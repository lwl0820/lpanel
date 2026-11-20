package panel

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type AccessChange struct {
	Host      *string
	Port      *int
	LoginPath *string
	HTTPS     *bool
	AgreeTOS  bool
}

func (a *App) ManageAccess(ctx context.Context, change AccessChange) (address string, err error) {
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()

	previous := a.currentConfig()
	if change.Host == nil && change.Port == nil && change.LoginPath == nil && change.HTTPS == nil {
		return managementURL(previous), nil
	}

	items, err := a.ListBindings(ctx)
	if err != nil {
		return "", err
	}
	var old Binding
	for _, item := range items {
		if item.Kind == "panel" {
			old = item
			break
		}
	}
	candidate := previous
	if change.Host != nil {
		host := strings.TrimSpace(*change.Host)
		if host == "auto" {
			candidate.PanelDomain = ""
		} else {
			candidate.PanelDomain, err = normalizeBindingDomain(host, "panel")
			if err != nil {
				return "", err
			}
		}
	}
	if change.Port != nil {
		candidate.PanelPort = *change.Port
	}
	if change.LoginPath != nil {
		candidate.LoginPath = *change.LoginPath
		if candidate.LoginPath == "/" {
			candidate.LoginPath = ""
		}
	}
	if change.HTTPS != nil {
		candidate.HTTPS = *change.HTTPS
	}
	if err = candidate.Validate(); err != nil {
		return "", err
	}
	if candidate.HTTPS {
		if candidate.PanelDomain == "" {
			return "", errors.New("启用 HTTPS 需要用 --host 指定域名或公网 IP。恢复自动地址时可同时用 --https off")
		}
		if candidate.PanelPort == 80 {
			return "", errors.New("面板使用 HTTPS 时不能占用证书验证端口 80")
		}
	}

	target := old
	if candidate.PanelDomain != old.Domain {
		target = Binding{}
		if candidate.PanelDomain != "" {
			for _, item := range items {
				if item.Domain == candidate.PanelDomain {
					return "", errors.New("该访问地址已被使用")
				}
			}
			target = Binding{ID: old.ID, Kind: "panel", ResourceID: "panel", Domain: candidate.PanelDomain, CertificateStatus: "disabled"}
			if target.ID == "" {
				target.ID, err = newBindingID()
				if err != nil {
					return "", err
				}
			}
		}
	}

	var challengeApplied, bindingChanged, configApplied, serviceRestarted bool
	defer func() {
		if err == nil {
			return
		}
		restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 25*time.Second)
		defer cancel()
		var restoreErr error
		if bindingChanged {
			if old.ID == "" {
				_, restoreErr = a.DB.ExecContext(restoreCtx, "DELETE FROM bindings WHERE id=?", target.ID)
			} else {
				restoreErr = a.storeBinding(restoreCtx, old)
			}
		}
		if challengeApplied || configApplied {
			restoreErr = errors.Join(restoreErr, a.applyNginxConfig(restoreCtx, previous))
		}
		if serviceRestarted {
			restoreErr = errors.Join(restoreErr, a.restartForAccess(restoreCtx, previous))
		}
		if restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("恢复原面板设置失败：%w", restoreErr))
		}
	}()

	if candidate.HTTPS {
		ip, _ := netip.ParseAddr(target.Domain)
		previousIP, _ := netip.ParseAddr(previous.PanelDomain)
		if ip.Is6() != previousIP.Is6() {
			if err = a.applyNginxBindings(ctx, previous, items, target.Domain); err != nil {
				return "", fmt.Errorf("准备证书验证入口失败：%w", err)
			}
			challengeApplied = true
		}
		if ip.IsValid() {
			target, err = a.issuePanelIPCertificate(ctx, target, change.AgreeTOS)
		} else {
			target, err = a.enableManagedDomainHTTPS(ctx, target, change.AgreeTOS)
		}
		if err != nil {
			return "", fmt.Errorf("启用 HTTPS 失败：%w", err)
		}
	} else if target.ID != "" {
		target.AutoCertificate = false
		target.CertificateStatus = "disabled"
		target.CertificateError = ""
	}
	if candidate == previous && target == old {
		return managementURL(previous), nil
	}
	if target != old {
		if target.ID == "" {
			_, err = a.DB.ExecContext(ctx, "DELETE FROM bindings WHERE id=?", old.ID)
		} else {
			err = a.storeBinding(ctx, target)
		}
		if err != nil {
			return "", fmt.Errorf("保存面板访问地址失败：%w", err)
		}
		bindingChanged = true
	}
	if err = a.applyNginxConfig(ctx, candidate); err != nil {
		if candidate.PanelPort != previous.PanelPort {
			return "", fmt.Errorf("切换面板端口 %d 失败，请检查端口是否被占用：%w", candidate.PanelPort, err)
		}
		return "", err
	}
	configApplied = true
	serviceRestarted = true
	if err = a.restartForAccess(ctx, candidate); err != nil {
		return "", err
	}
	return managementURL(candidate), nil
}

func (a *App) enableManagedDomainHTTPS(ctx context.Context, b Binding, agreeTOS bool) (Binding, error) {
	account, err := a.certificateAccount(ctx, "", b.CertificateAccountID)
	if err != nil {
		var exists bool
		if lookupErr := a.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM acme_accounts)").Scan(&exists); lookupErr != nil {
			return b, lookupErr
		}
		if exists {
			return b, err
		}
		if !agreeTOS {
			return b, errors.New("首次申请证书，请阅读 Let’s Encrypt 服务条款 https://letsencrypt.org/repository/ 后使用 --agree-tos 确认")
		}
		account, err = a.saveACMEAccountLocked(ctx, "", ACMEAccountInput{
			Name: "Let’s Encrypt", Provider: "letsencrypt", AgreeTOS: true,
		}, true)
		if err != nil {
			return b, err
		}
	}
	// 开关 HTTPS 后可以复用同一个账户下已有的有效证书。
	var certificate *x509.Certificate
	data, err := os.ReadFile(filepath.Join(a.certificateDir(account.ID, b.Domain), "fullchain.pem"))
	if err == nil {
		if block, _ := pem.Decode(data); block != nil {
			certificate, _ = x509.ParseCertificate(block.Bytes)
		}
	}
	now := time.Now()
	if certificate != nil && !now.Before(certificate.NotBefore) && now.Before(certificate.NotAfter) && certificate.VerifyHostname(b.Domain) == nil {
		b.ExpiresAt = certificate.NotAfter.UTC().Format(time.RFC3339)
	} else {
		b.ExpiresAt, err = a.issueDomainCertificate(ctx, b.Domain, account)
		if err != nil {
			return b, err
		}
	}
	b.AutoCertificate = true
	b.CertificateStatus = "enabled"
	b.CertificateAccountID = account.ID
	b.CertificateError = ""
	return b, nil
}

func (a *App) restartForAccess(ctx context.Context, config Config) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if output, err := a.Command(ctx, "systemctl", "restart", "lpanel.service"); err != nil {
		return fmt.Errorf("重启面板失败：%s：%w", strings.TrimSpace(string(output)), err)
	}
	if err := a.waitUpgradeHealth(ctx, a.Version); err != nil {
		return fmt.Errorf("面板服务重启后健康检查失败：%w", err)
	}
	address, host := localPanelEndpoint(config, managementLoginPath(config))
	if err := a.NginxProbe(ctx, address, host, ""); err != nil {
		return fmt.Errorf("面板登录入口检查失败：%w", err)
	}
	return nil
}

func managementLoginPath(config Config) string {
	if config.LoginPath == "" {
		return "/"
	}
	return config.LoginPath
}

func managementURL(config Config) string {
	host := config.PanelDomain
	if host == "" {
		host = "localhost"
		for _, ip := range serverIPs() {
			if ip.Is4() {
				host = ip.String()
				break
			}
			if host == "localhost" {
				host = ip.String()
			}
		}
	}
	binding := Binding{Kind: "panel", Domain: host}
	if config.HTTPS {
		binding.CertificateStatus = "enabled"
	}
	return bindingURL(binding, config.PanelPort) + managementLoginPath(config)
}

func serverIPs() []netip.Addr {
	addresses, _ := net.InterfaceAddrs()
	var ips []netip.Addr
	for _, address := range addresses {
		if prefix, err := netip.ParsePrefix(address.String()); err == nil && prefix.Addr().IsGlobalUnicast() {
			ips = append(ips, prefix.Addr().Unmap())
		}
	}
	return ips
}
