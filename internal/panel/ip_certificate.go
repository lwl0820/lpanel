package panel

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
)

const renewPanelIPBefore = 48 * time.Hour

var sharedIPv4Space = netip.MustParsePrefix("100.64.0.0/10")

func (a *App) issuePanelIPCertificate(ctx context.Context, b Binding, agreeTOS bool) (Binding, error) {
	ip, err := netip.ParseAddr(b.Domain)
	if err != nil || ip.Zone() != "" {
		return b, errors.New("请填写有效的公网 IP 地址")
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || sharedIPv4Space.Contains(ip) {
		return b, errors.New("IP 证书需要能通过公网验证的 IP 地址")
	}
	b.Domain = ip.String()

	account, err := scanACMEAccount(a.DB.QueryRowContext(ctx, selectACMEAccount+
		" WHERE provider='letsencrypt' AND server=? AND agree_tos=1 AND needs_eab=0 ORDER BY id=? DESC, seq LIMIT 1",
		letsEncryptServer, b.CertificateAccountID))
	if errors.Is(err, sql.ErrNoRows) {
		if !agreeTOS {
			return b, errors.New("请阅读 Let's Encrypt 服务条款 https://letsencrypt.org/repository/，同意后使用 --agree-tos 重试")
		}
		account, err = a.saveACMEAccountLocked(ctx, "", ACMEAccountInput{
			Name: getACMEProvider("letsencrypt").Name, Provider: "letsencrypt", AgreeTOS: true,
		}, true)
	}
	if err != nil {
		return b, fmt.Errorf("准备 Let's Encrypt 账户失败：%w", err)
	}

	dir := a.certificateDir(account.ID, b.Domain)
	certPath := filepath.Join(dir, "fullchain.pem")
	pair, pairErr := tls.LoadX509KeyPair(certPath, filepath.Join(dir, "privkey.pem"))
	var expiry time.Time
	if pairErr == nil {
		leaf, parseErr := x509.ParseCertificate(pair.Certificate[0])
		if parseErr == nil && !time.Now().Before(leaf.NotBefore) && leaf.VerifyHostname(b.Domain) == nil {
			expiry = leaf.NotAfter
		}
	}
	if !expiry.After(time.Now()) {
		var chain []byte
		chain, expiry, err = a.requestPanelIPCertificate(ctx, account, ip)
		if err != nil {
			return b, err
		}
		if err = a.replacePanelIPChain(certPath, chain); err != nil {
			return b, fmt.Errorf("保存 IP 证书失败：%w", err)
		}
	}

	b.AutoCertificate = true
	b.CertificateStatus = "enabled"
	b.CertificateAccountID = account.ID
	b.CertificateError = ""
	b.ExpiresAt = expiry.UTC().Format(time.RFC3339)
	return b, nil
}

func (a *App) renewPanelIPCertificate(ctx context.Context, b Binding) error {
	ip, err := netip.ParseAddr(b.Domain)
	if err != nil {
		return err
	}
	certPath := filepath.Join(a.certificateDir(b.CertificateAccountID, b.Domain), "fullchain.pem")
	previousChain, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("读取当前 IP 证书失败：%w", err)
	}
	block, _ := pem.Decode(previousChain)
	if block == nil {
		return errors.New("当前 IP 证书格式无效")
	}
	current, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("读取当前 IP 证书有效期失败：%w", err)
	}
	if time.Until(current.NotAfter) > renewPanelIPBefore {
		return nil
	}

	account, err := a.certificateAccount(ctx, b.CertificateAccountID, "")
	if err != nil {
		return err
	}
	if account.Provider != "letsencrypt" || account.server != letsEncryptServer {
		return errors.New("IP 证书需要使用 Let's Encrypt 账户")
	}
	chain, expiry, err := a.requestPanelIPCertificate(ctx, account, ip)
	if err != nil {
		return err
	}
	config, err := LoadConfig(a.Root)
	if err != nil {
		return fmt.Errorf("读取续期后的生效配置失败：%w", err)
	}
	if !config.HTTPS || config.PanelDomain != b.Domain {
		return nil
	}
	active, err := a.binding(ctx, b.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if active.Domain != b.Domain || active.CertificateStatus != "enabled" || active.CertificateAccountID != account.ID {
		return nil
	}
	if err = a.replacePanelIPChain(certPath, chain); err != nil {
		return fmt.Errorf("保存续期后的 IP 证书失败：%w", err)
	}
	leaf, _ := pem.Decode(chain)
	if err = a.reloadPanelIPCertificate(ctx, config, sha256.Sum256(leaf.Bytes)); err == nil {
		err = a.updateCertificateExpiry(ctx, b, expiry.UTC().Format(time.RFC3339))
	}
	if err == nil {
		return nil
	}

	if restoreErr := a.replacePanelIPChain(certPath, previousChain); restoreErr != nil {
		return errors.Join(err, fmt.Errorf("恢复原 IP 证书失败：%w", restoreErr))
	}
	return errors.Join(err, a.reloadPanelIPCertificate(context.WithoutCancel(ctx), config, sha256.Sum256(block.Bytes)))
}

func (a *App) reloadPanelIPCertificate(ctx context.Context, config Config, fingerprint [sha256.Size]byte) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// 续期以独立进程运行，仅重载现有文件，避免用启动时的设置覆盖新的面板入口。
	if output, err := a.Command(ctx, "nginx", "-t"); err != nil {
		return fmt.Errorf("Nginx 配置检查失败：%s：%w", strings.TrimSpace(string(output)), err)
	}
	if output, err := a.Command(ctx, "systemctl", "reload", "nginx"); err != nil {
		return fmt.Errorf("Nginx 重载失败：%s：%w", strings.TrimSpace(string(output)), err)
	}
	address := net.JoinHostPort(panelLoopback(config.PanelDomain), strconv.Itoa(config.PanelPort))
	if err := a.panelIPCertificateProbe(ctx, address, fingerprint); err != nil {
		return fmt.Errorf("Nginx 未加载预期 IP 证书：%w", err)
	}
	return nil
}

func probePanelIPCertificate(ctx context.Context, address string, fingerprint [sha256.Size]byte) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	dialer := tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 500 * time.Millisecond},
		Config: &tls.Config{
			// 本机回环探针直接核对刚保存的证书指纹。
			InsecureSkipVerify: true,
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.PeerCertificates) == 0 || sha256.Sum256(state.PeerCertificates[0].Raw) != fingerprint {
					return errors.New("返回的证书指纹不匹配")
				}
				return nil
			},
		},
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (a *App) requestPanelIPCertificate(ctx context.Context, account ACMEAccount, ip netip.Addr) ([]byte, time.Time, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	accountKey, err := readOrCreatePanelIPKey(filepath.Join(a.acmeConfigDir(account.ID), "ip-acme-account.pem"))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("读取或生成 IP 证书账户密钥失败：%w", err)
	}
	client := &acme.Client{Key: accountKey, DirectoryURL: account.server, HTTPClient: a.panelIPACMEHTTPClient}
	registration, err := client.GetReg(ctx, "")
	if errors.Is(err, acme.ErrNoAccount) {
		input := &acme.Account{}
		if account.Email != "" {
			input.Contact = []string{"mailto:" + account.Email}
		}
		registration, err = client.Register(ctx, input, func(string) bool { return account.AgreeTOS })
	}
	if err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return nil, time.Time{}, fmt.Errorf("准备 IP 证书签发账户失败：%w", err)
	}
	if registration != nil {
		client.KID = acme.KeyID(registration.URI)
	}
	key, err := readOrCreatePanelIPKey(filepath.Join(a.certificateDir(account.ID, ip.String()), "privkey.pem"))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("读取或生成 IP 证书私钥失败：%w", err)
	}

	chain, expiry, err := a.completePanelIPOrder(ctx, client, ip, key)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("申请 %s 的 IP 证书失败，请确认公网可访问该地址的 80 端口后重试：%w", ip, err)
	}
	return chain, expiry, nil
}

func (a *App) completePanelIPOrder(ctx context.Context, client *acme.Client, ip netip.Addr, key *ecdsa.PrivateKey) ([]byte, time.Time, error) {
	order, err := client.AuthorizeOrder(ctx, acme.IPIDs(ip.String()), acme.WithOrderIssuanceProfile("shortlived"))
	if err != nil {
		return nil, time.Time{}, err
	}
	for _, address := range order.AuthzURLs {
		auth, err := client.GetAuthorization(ctx, address)
		if err != nil {
			return nil, time.Time{}, err
		}
		if auth.Status == acme.StatusValid {
			continue
		}
		if auth.Status != acme.StatusPending {
			return nil, time.Time{}, fmt.Errorf("IP 验证状态异常：%s", auth.Status)
		}
		index := slices.IndexFunc(auth.Challenges, func(c *acme.Challenge) bool { return c.Type == "http-01" })
		if index < 0 {
			return nil, time.Time{}, errors.New("签发机构未提供 HTTP-01 验证方式")
		}
		challenge := auth.Challenges[index]
		if _, err := base64.RawURLEncoding.DecodeString(challenge.Token); err != nil ||
			challenge.Token == "" || strings.ContainsAny(challenge.Token, "\r\n") {
			return nil, time.Time{}, errors.New("签发机构返回的验证文件名无效")
		}
		reply, err := client.HTTP01ChallengeResponse(challenge.Token)
		if err != nil {
			return nil, time.Time{}, err
		}
		path := filepath.Join(a.Root, "acme-webroot", client.HTTP01ChallengePath(challenge.Token))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, time.Time{}, fmt.Errorf("创建 IP 验证目录失败：%w", err)
		}
		if err := os.WriteFile(path, []byte(reply), 0644); err != nil {
			return nil, time.Time{}, fmt.Errorf("写入 IP 验证文件失败：%w", err)
		}
		defer os.Remove(path)
		if _, err := client.Accept(ctx, challenge); err != nil {
			return nil, time.Time{}, err
		}
		if _, err := client.WaitAuthorization(ctx, address); err != nil {
			return nil, time.Time{}, err
		}
	}

	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return nil, time.Time{}, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{IPAddresses: []net.IP{ip.AsSlice()}}, key)
	if err != nil {
		return nil, time.Time{}, err
	}
	der, _, err := client.CreateCertFromOrder(ctx, order, csr, true)
	if err != nil {
		return nil, time.Time{}, err
	}
	if len(der) == 0 {
		return nil, time.Time{}, errors.New("签发机构未返回证书")
	}
	leaf, err := x509.ParseCertificate(der[0])
	if err != nil {
		return nil, time.Time{}, err
	}
	if err = leaf.VerifyHostname(ip.String()); err != nil {
		return nil, time.Time{}, fmt.Errorf("签发证书与面板 IP 不匹配：%w", err)
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) || !key.PublicKey.Equal(leaf.PublicKey) {
		return nil, time.Time{}, errors.New("签发证书的有效期或私钥不匹配")
	}
	var chain []byte
	for _, cert := range der {
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})...)
	}
	return chain, leaf.NotAfter, nil
}

func readOrCreatePanelIPKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, errors.New("密钥格式无效")
		}
		return x509.ParseECPrivateKey(block.Bytes)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	raw, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	data = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: raw})
	return key, os.WriteFile(path, data, 0600)
}

// 续期只替换证书链，同一个私钥继续供 Nginx 使用。
func (a *App) replacePanelIPChain(path string, chain []byte) error {
	file, err := os.CreateTemp(filepath.Join(a.Root, ".tmp"), "panel-ip-certificate-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(chain)
	if err = errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
