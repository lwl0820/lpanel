package panel

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	json "encoding/json/v2"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestACMEAccountDefaultAndDeletion(t *testing.T) {
	a, root := testApp(t)
	ctx := t.Context()
	cookie := call(a, http.MethodPost, "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil).Result().Cookies()[0]
	list, err := a.listACMEAccounts(ctx)
	if err != nil || list.DefaultAccountID != nil || len(list.Accounts) != 0 {
		t.Fatalf("HTTP 安装应无账户：%+v，%v", list, err)
	}
	first, err := a.CreateACMEAccount(ctx, ACMEAccountInput{Name: "第一个", Provider: "letsencrypt", AgreeTOS: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.saveACMEAccount(ctx, "", ACMEAccountInput{Name: "备用", Provider: "letsencrypt", AgreeTOS: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if list, err = a.listACMEAccounts(ctx); err != nil || list.DefaultAccountID == nil || *list.DefaultAccountID != first.ID {
		t.Fatalf("新增第一个账户后未成为默认：%+v，%v", list, err)
	}
	archive := filepath.Join(a.acmeConfigDir(first.ID), "archive", "old.example.com")
	if err = os.MkdirAll(archive, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(archive, "cert1.pem"), []byte("替身证书"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := call(a, http.MethodDelete, "/api/v1/acme/accounts/"+first.ID, "", cookie); w.Code != 409 || !strings.Contains(w.Body.String(), "此账户还有已签发的证书，无法删除") {
		t.Fatalf("未阻止删除保留历史证书的账户：%d %s", w.Code, w.Body)
	}
	if list, err = a.listACMEAccounts(ctx); err != nil || list.DefaultAccountID == nil || *list.DefaultAccountID != first.ID {
		t.Fatalf("拒绝删除后默认账户发生变化：%+v，%v", list, err)
	}
	if err = os.Remove(filepath.Join(archive, "cert1.pem")); err != nil {
		t.Fatal(err)
	}
	if err = a.deleteACMEAccount(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if list, err = a.listACMEAccounts(ctx); err != nil || list.DefaultAccountID == nil || *list.DefaultAccountID != second.ID {
		t.Fatalf("默认账户未切换至剩余账户：%+v，%v", list, err)
	}
	if err = a.deleteACMEAccount(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root, a.Version)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if list, err = reopened.listACMEAccounts(ctx); err != nil || list.DefaultAccountID != nil || len(list.Accounts) != 0 {
		t.Fatalf("删除最后一个账户后重启仍应无账户：%+v，%v", list, err)
	}
	replacement, err := reopened.CreateACMEAccount(ctx, ACMEAccountInput{Name: "新账户", Provider: "letsencrypt", AgreeTOS: true})
	if err != nil {
		t.Fatal(err)
	}
	if list, err = reopened.listACMEAccounts(ctx); err != nil || list.DefaultAccountID == nil || *list.DefaultAccountID != replacement.ID {
		t.Fatalf("重新创建首个账户后未成为默认：%+v，%v", list, err)
	}
}

func TestACMEZeroSSLRegistrationDoesNotExposeCredentials(t *testing.T) {
	a, _ := testApp(t)
	const secret = "test-hmac-secret"
	var temporaryConfig string
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "certbot" || args[0] != "register" || strings.Contains(strings.Join(args, " "), secret) {
			t.Fatal("注册命令错误或包含密钥")
		}
		configIndex := slices.Index(args, "--config")
		if configIndex < 0 {
			t.Fatal("缺少 Certbot 注册配置")
		}
		temporaryConfig = args[configIndex+1]
		data, err := os.ReadFile(temporaryConfig)
		if err != nil || !strings.Contains(string(data), secret) {
			t.Fatalf("注册配置未收到密钥：%v", err)
		}
		info, err := os.Stat(temporaryConfig)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("注册配置应仅管理员可读")
		}
		return []byte(secret), errors.New(secret)
	}
	cookie := call(a, http.MethodPost, "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil).Result().Cookies()[0]
	body := `{"name":"ZeroSSL 账户","provider":"zerossl","email":"admin@example.com","agree_tos":true,"eab_kid":"test-kid","eab_hmac_key":"` + secret + `"}`
	w := call(a, http.MethodPost, "/api/v1/acme/accounts", body, cookie)
	var account ACMEAccount
	if w.Code != 201 || strings.Contains(w.Body.String(), secret) || json.Unmarshal(w.Body.Bytes(), &account) != nil || !account.NeedsEAB {
		t.Fatalf("注册失败后应返回可补录的账户及状态：%d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(temporaryConfig); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("临时注册配置未清理：%v", err)
	}
	a.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	w = call(a, http.MethodPut, "/api/v1/acme/accounts/"+account.ID, body, cookie)
	if w.Code != 200 || strings.Contains(w.Body.String(), secret) || json.Unmarshal(w.Body.Bytes(), &account) != nil || account.NeedsEAB {
		t.Fatalf("重新注册后账户应可用于签发：%d %s", w.Code, w.Body)
	}
}

func TestACMECertificateSwitchAndRenewal(t *testing.T) {
	a := domainApp(t)
	ctx := t.Context()
	second, err := a.CreateACMEAccount(ctx, ACMEAccountInput{Name: "ZeroSSL 账户", Provider: "zerossl", AgreeTOS: true, EABKID: "test-kid", EABHMACKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := a.SaveBinding(ctx, Binding{Kind: "panel", Domain: "panel.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	certificate := acmeTestCertificate(t, b.Domain)
	failActivation := false
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "certbot" {
			return nil, nil
		}
		configIndex := slices.Index(args, "--config-dir")
		if configIndex < 0 {
			t.Fatal("缺少证书配置目录")
		}
		configDir := args[configIndex+1]
		if args[0] == "renew" {
			if configDir != a.acmeConfigDir(second.ID) || !strings.Contains(strings.Join(args, " "), "--cert-name "+b.Domain) {
				t.Fatalf("续期未沿用启用证书的账户与域名：%v", args)
			}
			return nil, nil
		}
		if configDir == a.acmeConfigDir(second.ID) && !slices.Contains(args, "https://acme.zerossl.com/v2/DV90") {
			t.Fatal("ZeroSSL 账户未使用其签发地址")
		}
		dir := filepath.Join(configDir, "live", b.Domain)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(dir, "fullchain.pem"), certificate, 0600)
	}
	a.NginxProbe = func(context.Context, string, string, string) error {
		if failActivation {
			return errors.New("测试切换失败")
		}
		return nil
	}
	if err = a.RequestCertificate(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	previous, err := a.binding(ctx, b.ID)
	if err != nil || previous.CertificateAccountID != legacyACMEID {
		t.Fatalf("首次申请未使用默认账户：%+v，%v", previous, err)
	}
	failActivation = true
	if err = a.RequestCertificateWithAccount(ctx, b.ID, second.ID); err == nil {
		t.Fatal("切换失败未报告")
	}
	current, err := a.binding(ctx, b.ID)
	if err != nil || current.CertificateAccountID != previous.CertificateAccountID || current.CertificateStatus != "enabled" {
		t.Fatalf("切换失败未保留原证书：%+v，%v", current, err)
	}
	if err = a.deleteACMEAccount(ctx, second.ID); !errors.Is(err, errACMEAccountInUse) {
		t.Fatalf("签发成功但启用失败的存留证书应阻止删除账户：%v", err)
	}
	failActivation = false
	if err = a.RequestCertificateWithAccount(ctx, b.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if current, err = a.binding(ctx, b.ID); err != nil || current.CertificateAccountID != second.ID {
		t.Fatalf("切换成功未保存新账户：%+v，%v", current, err)
	}
	if err = a.RenewCertificates(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestACMELegacyAccountImportPreservesCertificate(t *testing.T) {
	root := t.TempDir()
	config := DefaultConfig()
	config.AgreeTOS, config.ACMEEmail = true, "old@example.com"
	config.ACMEServer = "https://test.example.com/directory"
	if err := SaveConfig(root, config); err != nil {
		t.Fatal(err)
	}
	oldDB, err := sql.Open("sqlite", filepath.Join(root, "data", "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = oldDB.Exec(`CREATE TABLE bindings(id TEXT PRIMARY KEY,domain TEXT NOT NULL UNIQUE,payload TEXT NOT NULL);
		INSERT INTO bindings VALUES('panel-id', 'panel.example.com',
			'{"id":"panel-id","domain":"panel.example.com","kind":"panel","certificate_status":"enabled"}')`); err != nil {
		t.Fatal(err)
	}
	if err = oldDB.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := Open(root, "v0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	list, err := a.listACMEAccounts(t.Context())
	if err != nil || list.DefaultAccountID == nil || *list.DefaultAccountID != legacyACMEID || list.Accounts[0].server != config.ACMEServer || list.Accounts[0].Email != config.ACMEEmail {
		t.Fatalf("旧证书配置未作为默认账户保留：%+v，%v", list, err)
	}
	b, err := a.binding(t.Context(), "panel-id")
	if err != nil || b.CertificateAccountID != legacyACMEID || a.certificateDir(b.CertificateAccountID, b.Domain) != filepath.Join(root, "data/acme/live", "panel.example.com") {
		t.Fatalf("旧绑定应继续读取原证书目录：%+v，%v", b, err)
	}
	oldDir := filepath.Join(root, "data/acme/live", b.Domain)
	if err = os.MkdirAll(oldDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldCertificate := filepath.Join(oldDir, "fullchain.pem")
	if err = os.WriteFile(oldCertificate, acmeTestCertificate(t, b.Domain), 0600); err != nil {
		t.Fatal(err)
	}
	renewed := false
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "certbot" {
			return nil, nil
		}
		config := slices.Index(args, "--config-dir")
		domain := slices.Index(args, "--cert-name")
		if args[0] != "renew" || config < 0 || args[config+1] != filepath.Join(root, "data/acme") || domain < 0 || args[domain+1] != b.Domain {
			t.Fatalf("旧证书续期参数不一致：%v", args)
		}
		renewed = true
		return nil, nil
	}
	if err = a.RenewCertificates(t.Context()); err != nil || !renewed {
		t.Fatalf("旧证书未续期：%v", err)
	}
	if err = a.deleteACMEAccount(t.Context(), legacyACMEID); !errors.Is(err, errACMEAccountInUse) {
		t.Fatalf("启用证书的旧账户应禁止删除：%v", err)
	}
	if _, err = a.DB.Exec("DELETE FROM bindings WHERE id='panel-id'"); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(oldCertificate); err != nil {
		t.Fatal(err)
	}
	if err = a.deleteACMEAccount(t.Context(), legacyACMEID); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root, a.Version)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if list, err = reopened.listACMEAccounts(t.Context()); err != nil || list.DefaultAccountID != nil {
		t.Fatalf("删除旧账户后重启不应重新导入：%+v，%v", list, err)
	}
}

func acmeTestCertificate(t *testing.T, domain string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{domain}, NotBefore: time.Now(), NotAfter: time.Now().Add(24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
