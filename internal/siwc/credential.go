package siwc

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"claude_bot/internal/util"
)

const refreshMargin = time.Minute

var errDeploymentCredentialPath = errors.New("認証情報はデプロイで同期される data/ の外に保存してください")

type Credential struct {
	Email          string    `json:"email"`
	Issuer         string    `json:"issuer"`
	Subject        string    `json:"subject"`
	ClientID       string    `json:"client_id"`
	ExtAgentHostID string    `json:"ext_agent_host_id"`
	IDToken        string    `json:"id_token,omitempty"`
	AccessToken    string    `json:"access_token,omitempty"`
	RefreshToken   string    `json:"refresh_token,omitempty"`
	TokenType      string    `json:"token_type,omitempty"`
	ExpiresIn      int64     `json:"expires_in,omitempty"`
	Scopes         []string  `json:"scopes,omitempty"`
	SavedAt        time.Time `json:"saved_at"`
}

func ResolveCredentialPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("OPENAI_CREDENTIAL_FILE には絶対パスを指定してください")
	}
	dir := filepath.Dir(path)
	if isDeploymentDataDir(dir) {
		return "", errDeploymentCredentialPath
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("認証情報の保存先を確認できません: %w", err)
	}
	if isDeploymentDataDir(resolved) {
		return "", errDeploymentCredentialPath
	}
	path = filepath.Join(resolved, filepath.Base(path))
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("認証情報の保存先には通常ファイルを指定してください: %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("認証情報の保存先を確認できません: %w", err)
	}
	return path, nil
}

func LoadCredential(path string) (*Credential, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("認証情報読込失敗: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("認証情報の属性取得失敗: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("認証情報は所有者のみアクセスできる通常ファイルを指定してください: %s", path)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("認証情報読込失敗: %w", err)
	}
	var credential Credential
	if err := json.Unmarshal(data, &credential); err != nil {
		return nil, fmt.Errorf("認証情報ファイルを解析できません (%s): %w", path, err)
	}
	return &credential, nil
}

func SaveCredential(path string, credential *Credential) error {
	data, err := json.MarshalIndent(credential, "", "  ")
	if err != nil {
		return err
	}
	return writeProtectedFile(path, data)
}

func writeProtectedFile(path string, data []byte) error {
	path, err := ResolveCredentialPath(path)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func (c *Credential) AccessTokenExpiry() time.Time {
	return c.SavedAt.Add(time.Duration(c.ExpiresIn) * time.Second)
}

func (c *Credential) NeedsRefresh(now time.Time) bool {
	return c.AccessToken == "" || now.After(c.AccessTokenExpiry().Add(-refreshMargin))
}

func (c *Credential) HasPlanUsageScope() bool {
	return slices.Contains(c.Scopes, PlanUsageScope)
}

func (c *Credential) ClearTokens() {
	c.IDToken = ""
	c.AccessToken = ""
	c.RefreshToken = ""
	c.TokenType = ""
	c.ExpiresIn = 0
}

func HostIDPath(credentialPath string) string {
	return credentialPath + ".host_id"
}

func LoadOrCreateHostID(credentialPath string) (string, error) {
	path := HostIDPath(credentialPath)
	if data, err := os.ReadFile(path); err == nil {
		if hostID := strings.TrimSpace(string(data)); hostID != "" {
			return hostID, nil
		}
		return "", fmt.Errorf("ホストIDファイルが空です (%s)", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("ホストIDファイルを読み込めません (%s): %w", path, err)
	}
	hostID, err := newHostID()
	if err != nil {
		return "", fmt.Errorf("ホストIDを生成できません (%s): %w", path, err)
	}
	if err := writeProtectedFile(path, []byte(hostID)); err != nil {
		return "", fmt.Errorf("ホストIDを保存できません (%s): %w", path, err)
	}
	return hostID, nil
}

func newHostID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	uuid := fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
	return "urn:uuid:" + uuid, nil
}

func isDeploymentDataDir(dir string) bool {
	for root := dir; root != filepath.Dir(root); root = filepath.Dir(root) {
		if !containsDirectory(filepath.Join(root, util.DataDirName), dir) {
			continue
		}
		if info, err := os.Stat(filepath.Join(root, "deploy.sh")); err != nil || !info.Mode().IsRegular() {
			continue
		}
		mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(mod), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "module" && fields[1] == "claude_bot" {
				return true
			}
		}
	}
	return false
}

func containsDirectory(root, dir string) bool {
	relative, err := filepath.Rel(root, dir)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return true
	}
	info, err := os.Stat(root)
	if err != nil {
		return false
	}
	for parent := dir; parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
		if ancestor, err := os.Stat(parent); err == nil && os.SameFile(info, ancestor) {
			return true
		}
	}
	return false
}
