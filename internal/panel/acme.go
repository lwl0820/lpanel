package panel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"uuid"
)

const (
	letsEncryptServer = "https://acme-v02.api.letsencrypt.org/directory"
	legacyACMEID      = "legacy"
)

var acmeProviders = []acmeProvider{
	{ID: "letsencrypt", Name: "Let's Encrypt", TOSURL: "https://letsencrypt.org/repository/", server: letsEncryptServer},
	{ID: "zerossl", Name: "ZeroSSL", TOSURL: "https://zerossl.com/legal/terms", RequiresEAB: true, server: "https://acme.zerossl.com/v2/DV90"},
}

var errACMEAccountInUse = errors.New("此账户还有已签发的证书，无法删除")

type acmeProvider struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	TOSURL      string `json:"tos_url"`
	RequiresEAB bool   `json:"requires_eab"`
	server      string
}

type ACMEAccount struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	ProviderName string `json:"provider_name"`
	Email        string `json:"email"`
	AgreeTOS     bool   `json:"agree_tos"`
	NeedsEAB     bool   `json:"needs_eab"`
	server       string
}

type ACMEAccountInput struct {
	Name       string `json:"name"`
	Provider   string `json:"provider"`
	Email      string `json:"email"`
	AgreeTOS   bool   `json:"agree_tos"`
	EABKID     string `json:"eab_kid"`
	EABHMACKey string `json:"eab_hmac_key"`
	Server     string `json:"-"`
}

type acmeAccountList struct {
	Accounts         []ACMEAccount  `json:"accounts"`
	DefaultAccountID *string        `json:"default_account_id"`
	Providers        []acmeProvider `json:"providers"`
}

func getACMEProvider(id string) acmeProvider {
	for _, provider := range acmeProviders {
		if provider.ID == id {
			return provider
		}
	}
	return acmeProvider{}
}

const selectACMEAccount = "SELECT id,name,provider,email,agree_tos,needs_eab,server FROM acme_accounts"

func scanACMEAccount(row interface{ Scan(...any) error }) (ACMEAccount, error) {
	var a ACMEAccount
	err := row.Scan(&a.ID, &a.Name, &a.Provider, &a.Email, &a.AgreeTOS, &a.NeedsEAB, &a.server)
	a.ProviderName = getACMEProvider(a.Provider).Name
	if a.Provider == "legacy" {
		a.ProviderName = "自定义 ACME"
	}
	return a, err
}

func (a *App) listACMEAccounts(ctx context.Context) (acmeAccountList, error) {
	list := acmeAccountList{Accounts: []ACMEAccount{}, Providers: acmeProviders}
	rows, err := a.DB.QueryContext(ctx, selectACMEAccount+" ORDER BY seq")
	if err != nil {
		return list, err
	}
	defer rows.Close()
	for rows.Next() {
		account, err := scanACMEAccount(rows)
		if err != nil {
			return list, err
		}
		list.Accounts = append(list.Accounts, account)
	}
	if len(list.Accounts) > 0 {
		list.DefaultAccountID = new(list.Accounts[0].ID)
	}
	return list, rows.Err()
}

func (a *App) registerACMEAccounts() error {
	if _, err := a.DB.Exec(`
		CREATE TABLE IF NOT EXISTS acme_accounts (
			seq INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, name TEXT NOT NULL,
			provider TEXT NOT NULL, email TEXT NOT NULL, agree_tos INTEGER NOT NULL,
			needs_eab INTEGER NOT NULL, server TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS acme_legacy_import (id INTEGER PRIMARY KEY CHECK(id=1));
	`); err != nil {
		return err
	}
	if err := a.importLegacyACMEAccount(); err != nil {
		return err
	}
	a.Mux.Handle("GET /api/v1/acme/accounts", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		list, err := a.listACMEAccounts(r.Context())
		if err != nil {
			WriteError(w, 500, "acme_error", "读取证书账户失败")
			return
		}
		WriteJSON(w, 200, list)
	})))
	a.Mux.Handle("POST /api/v1/acme/accounts", a.RequireAuth(http.HandlerFunc(a.saveACMEAccountRequest)))
	a.Mux.Handle("PUT /api/v1/acme/accounts/{id}", a.RequireAuth(http.HandlerFunc(a.saveACMEAccountRequest)))
	a.Mux.Handle("DELETE /api/v1/acme/accounts/{id}", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := a.deleteACMEAccount(r.Context(), r.PathValue("id"))
		switch {
		case errors.Is(err, errACMEAccountInUse):
			WriteError(w, 409, "acme_account_in_use", err.Error())
		case errors.Is(err, sql.ErrNoRows):
			WriteError(w, 404, "not_found", "证书账户不存在")
		case err != nil:
			WriteError(w, 500, "acme_error", "无法检查证书或删除账户，请查看服务日志")
		default:
			WriteJSON(w, 200, map[string]bool{"ok": true})
		}
	})))
	return nil
}

func (a *App) saveACMEAccountRequest(w http.ResponseWriter, r *http.Request) {
	var input ACMEAccountInput
	if !ReadJSON(w, r, &input) {
		return
	}
	account, err := a.saveACMEAccount(r.Context(), r.PathValue("id"), input, false)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		WriteError(w, 404, "not_found", "证书账户不存在")
	case r.Method == http.MethodPost && account.ID != "":
		WriteJSON(w, 201, account)
	case err != nil:
		WriteError(w, 400, "acme_error", err.Error())
	default:
		WriteJSON(w, 200, account)
	}
}

func (a *App) CreateACMEAccount(ctx context.Context, input ACMEAccountInput) (ACMEAccount, error) {
	return a.saveACMEAccount(ctx, "", input, true)
}

func (a *App) saveACMEAccount(ctx context.Context, id string, input ACMEAccountInput, reuseExisting bool) (ACMEAccount, error) {
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()

	name, email := strings.TrimSpace(input.Name), strings.TrimSpace(input.Email)
	if name == "" || len(name) > 100 {
		return ACMEAccount{}, errors.New("请填写账户名称，长度不能超过 100 字节")
	}
	if email != "" {
		address, err := mail.ParseAddress(email)
		if err != nil || address.Address != email {
			return ACMEAccount{}, errors.New("请输入有效的联系邮箱")
		}
	}
	kid, key := strings.TrimSpace(input.EABKID), strings.TrimSpace(input.EABHMACKey)
	hasCredentials := kid != "" && key != ""
	if (kid == "") != (key == "") || strings.ContainsAny(kid+key, "\r\n") {
		return ACMEAccount{}, errors.New("请完整填写账户绑定凭据")
	}

	provider := getACMEProvider(input.Provider)
	if input.Provider == "legacy" && input.Server != "" {
		provider = acmeProvider{ID: "legacy", Name: "自定义 ACME", server: input.Server}
	}
	if reuseExisting && provider.ID != "" {
		current, err := scanACMEAccount(a.DB.QueryRowContext(ctx, selectACMEAccount+" WHERE provider=? AND server=? AND email=? AND agree_tos=? ORDER BY seq LIMIT 1",
			provider.ID, provider.server, email, input.AgreeTOS))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return ACMEAccount{}, err
		}
		if err == nil {
			if !current.NeedsEAB {
				return current, nil
			}
			id, name = current.ID, current.Name
		}
	}
	account := ACMEAccount{ID: id, Name: name, Provider: input.Provider, Email: email, AgreeTOS: input.AgreeTOS}
	if id == "" {
		if provider.ID == "" {
			return ACMEAccount{}, errors.New("请选择有效的证书提供商")
		}
		account.ID = uuid.New().String()
		account.ProviderName, account.server = provider.Name, provider.server
		account.NeedsEAB = provider.RequiresEAB
		if account.NeedsEAB && !hasCredentials {
			return ACMEAccount{}, errors.New("请填写 ZeroSSL 账户绑定凭据")
		}
		if account.NeedsEAB && !account.AgreeTOS {
			return ACMEAccount{}, errors.New("请先同意 ZeroSSL 服务条款")
		}
		if _, err := a.DB.ExecContext(ctx, "INSERT INTO acme_accounts(id,name,provider,email,agree_tos,needs_eab,server) VALUES(?,?,?,?,?,?,?)",
			account.ID, account.Name, account.Provider, account.Email, account.AgreeTOS, account.NeedsEAB, account.server); err != nil {
			return ACMEAccount{}, errors.New("保存证书账户失败")
		}
	} else {
		current, err := scanACMEAccount(a.DB.QueryRowContext(ctx, selectACMEAccount+" WHERE id=?", id))
		if err != nil {
			return ACMEAccount{}, err
		}
		if input.Provider != "" && input.Provider != current.Provider {
			return ACMEAccount{}, errors.New("请添加新账户以使用其他证书提供商")
		}
		account.Provider, account.ProviderName = current.Provider, current.ProviderName
		account.NeedsEAB, account.server = current.NeedsEAB, current.server
		if hasCredentials && account.NeedsEAB && !account.AgreeTOS {
			return ACMEAccount{}, errors.New("请先同意 ZeroSSL 服务条款")
		}
		if _, err = a.DB.ExecContext(ctx, "UPDATE acme_accounts SET name=?,email=?,agree_tos=? WHERE id=?", name, email, input.AgreeTOS, id); err != nil {
			return ACMEAccount{}, errors.New("保存证书账户失败")
		}
	}

	if account.NeedsEAB && hasCredentials {
		if err := a.registerEABAccount(ctx, account, kid, key); err != nil {
			return account, err
		}
		if _, err := a.DB.ExecContext(ctx, "UPDATE acme_accounts SET needs_eab=0 WHERE id=?", account.ID); err != nil {
			return account, errors.New("保存证书账户注册状态失败")
		}
		account.NeedsEAB = false
	}
	return account, nil
}

func (a *App) registerEABAccount(ctx context.Context, account ACMEAccount, kid, key string) error {
	dir, err := os.MkdirTemp(filepath.Join(a.Root, ".tmp"), "acme-register-*")
	if err != nil {
		return errors.New("无法准备 ZeroSSL 账户注册")
	}
	defer os.RemoveAll(dir)
	config := filepath.Join(dir, "certbot.ini")
	if err = os.WriteFile(config, fmt.Appendf(nil, "eab-kid = %s\neab-hmac-key = %s\n", kid, key), 0600); err != nil {
		return errors.New("无法准备 ZeroSSL 账户注册")
	}
	args := append([]string{"register", "--non-interactive", "--agree-tos", "--config", config, "--logs-dir", dir}, a.certbotDirs(account.ID)...)
	args = append(args, "--server", account.server)
	args = append(args, certbotContact(account.Email)...)
	if _, err = a.Command(ctx, "certbot", args...); err != nil {
		return errors.New("无法注册 ZeroSSL 账户，请检查账户绑定凭据和网络连接后重试")
	}
	return nil
}

func certbotContact(email string) []string {
	if email != "" {
		return []string{"--email", email}
	}
	return []string{"--register-unsafely-without-email"}
}

func legacyACMEValues(config Config) (provider, name, server string) {
	if config.ACMEServer != "" && config.ACMEServer != letsEncryptServer {
		return "legacy", "自定义 ACME", config.ACMEServer
	}
	return "letsencrypt", "Let's Encrypt", letsEncryptServer
}

func (a *App) importLegacyACMEAccount() error {
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("INSERT OR IGNORE INTO acme_legacy_import(id) VALUES(1)")
	if err != nil {
		return err
	}
	imported, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if imported == 0 {
		return tx.Commit()
	}
	var enabled bool
	if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM bindings WHERE json_extract(payload,'$.certificate_status')='enabled')").Scan(&enabled); err != nil {
		return err
	}
	stored, err := acmeCertificateStored(a.acmeConfigDir(legacyACMEID))
	if err != nil {
		return err
	}
	if enabled || stored || a.Config.AgreeTOS || a.Config.ACMEEmail != "" || a.Config.ACMEServer != "" {
		provider, name, server := legacyACMEValues(a.Config)
		if _, err = tx.Exec("INSERT INTO acme_accounts(id,name,provider,email,agree_tos,needs_eab,server) VALUES(?,?,?,?,?,0,?)",
			legacyACMEID, name, provider, a.Config.ACMEEmail, a.Config.AgreeTOS, server); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE bindings SET payload=json_set(payload,'$.certificate_account_id',?) WHERE json_extract(payload,'$.certificate_status')='enabled' AND COALESCE(json_extract(payload,'$.certificate_account_id'),'')=''", legacyACMEID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *App) saveLegacyAccessConfig(ctx context.Context, config Config) error {
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if config.AgreeTOS || config.ACMEEmail != "" || config.ACMEServer != "" {
		provider, name, server := legacyACMEValues(config)
		var currentServer string
		err = tx.QueryRowContext(ctx, "SELECT server FROM acme_accounts WHERE id=?", legacyACMEID).Scan(&currentServer)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && currentServer != server {
			stored, err := acmeCertificateStored(a.acmeConfigDir(legacyACMEID))
			if err != nil {
				return err
			}
			if stored {
				return errors.New("此账户已有证书，请添加新账户以使用其他证书提供商")
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO acme_accounts(id,name,provider,email,agree_tos,needs_eab,server) VALUES(?,?,?,?,?,0,?)
			ON CONFLICT(id) DO UPDATE SET name=CASE WHEN provider<>excluded.provider THEN excluded.name ELSE name END,
			provider=excluded.provider, email=excluded.email, agree_tos=excluded.agree_tos, server=excluded.server`,
			legacyACMEID, name, provider, config.ACMEEmail, config.AgreeTOS, server)
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE acme_accounts SET email='',agree_tos=0 WHERE id=?", legacyACMEID)
	}
	if err != nil {
		return err
	}
	if err = SaveConfig(a.Root, config); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	a.Config = config
	return nil
}

func (a *App) certificateAccount(ctx context.Context, explicitID, currentID string) (ACMEAccount, error) {
	var account ACMEAccount
	var err error
	switch {
	case explicitID != "":
		account, err = scanACMEAccount(a.DB.QueryRowContext(ctx, selectACMEAccount+" WHERE id=?", explicitID))
		if errors.Is(err, sql.ErrNoRows) {
			return account, errors.New("找不到所选证书账户，请重新选择")
		}
	case currentID != "":
		account, err = scanACMEAccount(a.DB.QueryRowContext(ctx, selectACMEAccount+" WHERE id=?", currentID))
		if !errors.Is(err, sql.ErrNoRows) {
			break
		}
		fallthrough
	default:
		account, err = scanACMEAccount(a.DB.QueryRowContext(ctx, selectACMEAccount+" ORDER BY seq LIMIT 1"))
		if errors.Is(err, sql.ErrNoRows) {
			return account, errors.New("请先在 ACME 账户管理中添加证书账户")
		}
	}
	if err != nil {
		return account, err
	}
	if !account.AgreeTOS {
		return account, errors.New("请先同意所选证书账户的服务条款")
	}
	if account.NeedsEAB {
		return account, errors.New("请先在 ACME 账户管理中完成 ZeroSSL 账户配置")
	}
	return account, nil
}

func (a *App) acmeConfigDir(accountID string) string {
	if accountID == "" || accountID == legacyACMEID {
		return filepath.Join(a.Root, "data/acme")
	}
	return filepath.Join(a.Root, "data/acme-accounts", accountID)
}

func (a *App) certificateDir(accountID, domain string) string {
	return filepath.Join(a.acmeConfigDir(accountID), "live", domain)
}

func (a *App) certbotDirs(accountID string) []string {
	return []string{"--config-dir", a.acmeConfigDir(accountID), "--work-dir", filepath.Join(a.Root, ".tmp/acme", accountID)}
}

func acmeCertificateStored(configDir string) (bool, error) {
	domains, err := os.ReadDir(filepath.Join(configDir, "archive"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	for _, domain := range domains {
		if !domain.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(configDir, "archive", domain.Name()))
		if err != nil {
			return false, err
		}
		for _, file := range files {
			if strings.HasPrefix(file.Name(), "cert") && strings.HasSuffix(file.Name(), ".pem") {
				return true, nil
			}
		}
	}
	domains, err = os.ReadDir(filepath.Join(configDir, "live"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	for _, domain := range domains {
		if !domain.IsDir() {
			continue
		}
		if _, err = os.Stat(filepath.Join(configDir, "live", domain.Name(), "fullchain.pem")); err == nil {
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	return false, nil
}

func (a *App) deleteACMEAccount(ctx context.Context, id string) error {
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	if err := a.DB.QueryRowContext(ctx, "SELECT id FROM acme_accounts WHERE id=?", id).Scan(&id); err != nil {
		return err
	}
	stored, err := acmeCertificateStored(a.acmeConfigDir(id))
	if err != nil {
		return err
	}
	if stored {
		return errACMEAccountInUse
	}
	var inUse bool
	if err = a.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM bindings
		WHERE json_extract(payload,'$.certificate_status')='enabled'
		AND COALESCE(NULLIF(json_extract(payload,'$.certificate_account_id'),''),?)=?)`, legacyACMEID, id).Scan(&inUse); err != nil {
		return err
	}
	if inUse {
		return errACMEAccountInUse
	}
	_, err = a.DB.ExecContext(ctx, "DELETE FROM acme_accounts WHERE id=?", id)
	return err
}
