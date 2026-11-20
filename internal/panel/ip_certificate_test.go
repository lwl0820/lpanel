package panel

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	json "encoding/json/v2"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestACMEPanelIPCertificateAndRenewal(t *testing.T) {
	a, root := testApp(t)
	ctx := t.Context()
	const ip = "2001:db8::8"
	const caURL = "https://acme-v02.api.letsencrypt.org"
	const token = "test-ip-verification-token"
	challengePath := filepath.Join(root, "acme-webroot", ".well-known", "acme-challenge", token)
	config := a.currentConfig()
	config.NginxConfigDir = filepath.Join(root, "nginx")
	a.setConfig(config)
	a.Command = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name != "nginx" && name != "systemctl" {
			t.Fatalf("IP 证书调用了外部签发工具：%s", name)
		}
		return nil, nil
	}
	a.NginxProbe = func(context.Context, string, string, string) error { return nil }
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(1, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, caKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	expiry := now.Add(24 * time.Hour)
	var orderCount int64
	requestCount := 0
	registered := false
	authorized := false
	var chain []byte
	a.panelIPACMEHTTPClient = &http.Client{Transport: panelIPTestTransport(func(r *http.Request) (*http.Response, error) {
		requestCount++
		w := httptest.NewRecorder()
		w.Header().Set("Replay-Nonce", base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "acme-nonce-%d", requestCount)))
		var payload []byte
		if r.Method == http.MethodPost {
			var jws struct {
				Payload string `json:"payload"`
			}
			if err := json.UnmarshalRead(r.Body, &jws); err != nil {
				t.Fatal(err)
			}
			var err error
			payload, err = base64.RawURLEncoding.DecodeString(jws.Payload)
			if err != nil {
				t.Fatal(err)
			}
		}
		switch r.URL.Path {
		case "/directory":
			_, _ = fmt.Fprintf(w, `{"newNonce":%q,"newAccount":%q,"newOrder":%q,"meta":{"termsOfService":%q,"profiles":{"shortlived":"有效期 160 小时"}}}`,
				caURL+"/nonce", caURL+"/new-account", caURL+"/new-order", caURL+"/terms")
		case "/nonce":
			w.WriteHeader(http.StatusOK)
		case "/new-account":
			var input struct {
				OnlyExisting bool `json:"onlyReturnExisting"`
				AgreeTOS     bool `json:"termsOfServiceAgreed"`
			}
			if err := json.Unmarshal(payload, &input); err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Location", caURL+"/account/1")
			switch {
			case input.OnlyExisting && !registered:
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.WriteString(`{"type":"urn:ietf:params:acme:error:accountDoesNotExist"}`)
			case input.OnlyExisting:
				_, _ = w.WriteString(`{"status":"valid"}`)
			default:
				if !input.AgreeTOS || registered {
					t.Fatalf("账户条款未确认或重复注册：%s", payload)
				}
				registered = true
				w.WriteHeader(http.StatusCreated)
				_, _ = w.WriteString(`{"status":"valid"}`)
			}
		case "/new-order":
			want := fmt.Sprintf(`{"identifiers":[{"type":"ip","value":%q}],"profile":"shortlived"}`, ip)
			if string(payload) != want {
				t.Fatalf("IP 订单 = %s，期望 %s", payload, want)
			}
			orderCount++
			authorized = false
			w.Header().Set("Location", caURL+"/order/1")
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"status":"pending","authorizations":[%q],"finalize":%q}`, caURL+"/auth", caURL+"/finalize")
		case "/auth":
			if authorized {
				_, _ = w.WriteString(`{"status":"valid"}`)
			} else {
				_, _ = fmt.Fprintf(w, `{"status":"pending","challenges":[{"type":"http-01","url":%q,"token":%q}]}`, caURL+"/challenge", token)
			}
		case "/challenge":
			content, err := os.ReadFile(challengePath)
			if err != nil || !strings.HasPrefix(string(content), token+".") {
				t.Fatalf("验证文件不可用：%s，%v", content, err)
			}
			authorized = true
			_, _ = w.WriteString(`{"status":"processing"}`)
		case "/order/1":
			if !authorized {
				t.Fatal("IP 验证通过前轮询订单")
			}
			_, _ = fmt.Fprintf(w, `{"status":"ready","finalize":%q}`, caURL+"/finalize")
		case "/finalize":
			var input struct {
				CSR string `json:"csr"`
			}
			if err := json.Unmarshal(payload, &input); err != nil {
				t.Fatal(err)
			}
			csrBytes, err := base64.RawURLEncoding.DecodeString(input.CSR)
			if err != nil {
				t.Fatal(err)
			}
			csr, err := x509.ParseCertificateRequest(csrBytes)
			if err != nil {
				t.Fatal(err)
			}
			if len(csr.IPAddresses) != 1 || csr.IPAddresses[0].String() != ip || len(csr.DNSNames) != 0 {
				t.Fatalf("证书请求 SAN 不正确：IP %v，域名 %v", csr.IPAddresses, csr.DNSNames)
			}
			leaf := &x509.Certificate{
				SerialNumber: big.NewInt(orderCount + 1), NotBefore: now.Add(-time.Minute), NotAfter: expiry,
				IPAddresses: csr.IPAddresses, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			}
			der, err := x509.CreateCertificate(rand.Reader, leaf, ca, csr.PublicKey, caKey)
			if err != nil {
				t.Fatal(err)
			}
			chain = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
			chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})...)
			_, _ = fmt.Fprintf(w, `{"status":"valid","certificate":%q}`, caURL+"/certificate")
		case "/certificate":
			_, _ = w.Write(chain)
		default:
			t.Fatalf("意外的 ACME 请求：%s", r.URL)
		}
		return w.Result(), nil
	})}

	panelBinding := Binding{ID: "panel-ip", Kind: "panel", ResourceID: "panel", Domain: ip}
	if _, err := a.issuePanelIPCertificate(ctx, panelBinding, false); err == nil ||
		!strings.Contains(err.Error(), "--agree-tos") || requestCount != 0 {
		t.Fatalf("首次签发未等待确认条款：%v，ACME 请求 %d", err, requestCount)
	}
	b, err := a.issuePanelIPCertificate(ctx, panelBinding, true)
	if err != nil {
		t.Fatal(err)
	}
	if b.CertificateStatus != "enabled" || b.CertificateAccountID == "" || b.ExpiresAt != expiry.Format(time.RFC3339) {
		t.Fatalf("签发状态错误：%+v", b)
	}
	if _, err := os.Stat(challengePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("验证文件未清理：%v", err)
	}
	dir := a.certificateDir(b.CertificateAccountID, b.Domain)
	certPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")
	previousChain, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	previousKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.storeBinding(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := a.ApplyNginx(ctx); err != nil {
		t.Fatal(err)
	}
	cached, err := a.issuePanelIPCertificate(ctx, b, false)
	if err != nil || cached.ExpiresAt != b.ExpiresAt || orderCount != 1 {
		t.Fatalf("未复用现有 IP 证书：%+v，订单 %d，%v", cached, orderCount, err)
	}

	// 登录设置可由其他进程修改。续期用最新端口核对证书，保留原入口文件。
	latest, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	latest.PanelPort = 9443
	latest.LoginPath = "/new-login-entry"
	if err := SaveConfig(root, latest); err != nil {
		t.Fatal(err)
	}
	a.NginxProbe = func(context.Context, string, string, string) error {
		t.Fatal("续期不应重新生成 Nginx 配置")
		return nil
	}
	failReload := true
	a.panelIPCertificateProbe = func(_ context.Context, address string, fingerprint [sha256.Size]byte) error {
		if address != "[::1]:9443" {
			t.Fatalf("未使用最新面板端口：%q", address)
		}
		data, err := os.ReadFile(certPath)
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(data)
		if block == nil || sha256.Sum256(block.Bytes) != fingerprint {
			t.Fatal("Nginx 未核对本次重载的证书指纹")
		}
		if failReload {
			failReload = false
			return errors.New("Nginx 未加载新证书")
		}
		return nil
	}
	expiry = now.Add(160 * time.Hour)
	if err := a.RenewCertificates(ctx); err == nil || !strings.Contains(err.Error(), "Nginx 未加载新证书") {
		t.Fatalf("续期未报告加载失败：%v", err)
	}
	actualChain, err := os.ReadFile(certPath)
	if err != nil || !bytes.Equal(actualChain, previousChain) {
		t.Fatalf("加载失败时未恢复原证书：%v", err)
	}
	stored, err := a.binding(ctx, b.ID)
	if err != nil || stored.ExpiresAt != b.ExpiresAt {
		t.Fatalf("加载失败时改写了到期时间：%+v，%v", stored, err)
	}
	if err := a.RenewCertificates(ctx); err != nil {
		t.Fatal(err)
	}
	if saved, err := LoadConfig(root); err != nil || saved != latest {
		t.Fatalf("续期改写了最新的登录设置：%+v，%v", saved, err)
	}
	stored, err = a.binding(ctx, b.ID)
	if err != nil || stored.ExpiresAt != expiry.Format(time.RFC3339) || orderCount != 3 {
		t.Fatalf("续期结果错误：%+v，订单 %d，%v", stored, orderCount, err)
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil || len(pair.Certificate) != 2 {
		t.Fatalf("续期后证书链无法加载：%v", err)
	}
	actualKey, err := os.ReadFile(keyPath)
	if err != nil || !bytes.Equal(actualKey, previousKey) {
		t.Fatalf("续期改变了已有证书的私钥：%v", err)
	}
	if err := a.RenewCertificates(ctx); err != nil || orderCount != 3 {
		t.Fatalf("续期窗口外发起了订单：订单 %d，%v", orderCount, err)
	}
}

func TestACMEPanelIPCertificateProbe(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	address := server.Listener.Addr().String()
	fingerprint := sha256.Sum256(server.Certificate().Raw)
	if err := probePanelIPCertificate(t.Context(), address, fingerprint); err != nil {
		t.Fatalf("没有识别 Nginx 正在提供的证书：%v", err)
	}
	fingerprint[0] ^= 1
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := probePanelIPCertificate(ctx, address, fingerprint); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("证书指纹不符时应重试直至超时：%v", err)
	}
}

type panelIPTestTransport func(*http.Request) (*http.Response, error)

func (transport panelIPTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}
