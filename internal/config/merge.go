package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BackupFile copies path to path.bak.<timestamp> (or backupDir if non-empty).
func BackupFile(path, backupDir string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	var dest string
	if strings.TrimSpace(backupDir) != "" {
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			return "", err
		}
		dest = filepath.Join(backupDir, filepath.Base(path)+"."+stamp)
	} else {
		dest = path + ".bak." + stamp
	}
	if err := os.WriteFile(dest, b, 0o600); err != nil {
		return "", err
	}
	return dest, nil
}

// MergeClientJSON merges patch JSON into base, preserving secrets when omitted or masked.
func MergeClientJSON(base *Client, patchJSON []byte, replace bool) (*Client, error) {
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(patchJSON, &patch); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	out := *base
	if replace {
		out = Client{}
	}
	raw, _ := json.Marshal(out)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if err := json.Unmarshal(patchJSON, &m); err != nil {
		return nil, err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var merged Client
	if err := json.Unmarshal(b, &merged); err != nil {
		return nil, err
	}
	preserveClientSecrets(base, &merged, patch)
	return NormalizeClient(&merged)
}

// MergeServerJSON merges patch JSON into base, preserving secrets when omitted or masked.
func MergeServerJSON(base *Server, patchJSON []byte, replace bool) (*Server, error) {
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(patchJSON, &patch); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	// Round-trip via map so omitted fields keep base values on PATCH.
	raw, _ := json.Marshal(base)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if replace {
		m = map[string]any{}
	}
	if err := json.Unmarshal(patchJSON, &m); err != nil {
		return nil, err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "lynx-server-*.json")
	if err != nil {
		return nil, err
	}
	name := tmp.Name()
	_, _ = tmp.Write(b)
	_ = tmp.Close()
	defer os.Remove(name)
	merged, err := LoadServer(name)
	if err != nil {
		return nil, err
	}
	preserveServerSecrets(base, merged, patch)
	// Re-load after secret restore so fingerprints stay normalized.
	b2, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	tmp2, err := os.CreateTemp("", "lynx-server-*.json")
	if err != nil {
		return nil, err
	}
	name2 := tmp2.Name()
	_, _ = tmp2.Write(b2)
	_ = tmp2.Close()
	defer os.Remove(name2)
	return LoadServer(name2)
}

func preserveClientSecrets(base, merged *Client, patch map[string]json.RawMessage) {
	if !fieldPresent(patch, "key") || isMaskedJSON(patch["key"]) {
		merged.Key = base.Key
	}
	if !fieldPresent(patch, "certificate") || isMaskedJSON(patch["certificate"]) {
		merged.Certificate = base.Certificate
	}
	if !fieldPresent(patch, "certificate_authority") || isMaskedJSON(patch["certificate_authority"]) {
		merged.CertificateAuthority = base.CertificateAuthority
	}
	if !fieldPresent(patch, "proxy_password") || isMaskedJSON(patch["proxy_password"]) {
		merged.ProxyPassword = base.ProxyPassword
	}
	if !fieldPresent(patch, "cf_access_client_secret") || isMaskedJSON(patch["cf_access_client_secret"]) {
		merged.CFAccessClientSecret = base.CFAccessClientSecret
	}
	if mgmtRaw, ok := patch["mgmt"]; ok {
		var mgmt map[string]json.RawMessage
		_ = json.Unmarshal(mgmtRaw, &mgmt)
		if !fieldPresent(mgmt, "secret") || isMaskedJSON(mgmt["secret"]) {
			merged.Mgmt.Secret = base.Mgmt.Secret
		}
	} else if !fieldPresent(patch, "mgmt") {
		merged.Mgmt.Secret = base.Mgmt.Secret
	}
	if !fieldPresent(patch, "subscribe_url") || strings.Contains(merged.SubscribeURL, "***") {
		if strings.Contains(merged.SubscribeURL, "***") || !fieldPresent(patch, "subscribe_url") {
			merged.SubscribeURL = base.SubscribeURL
		}
	}
}

func preserveServerSecrets(base, merged *Server, patch map[string]json.RawMessage) {
	if mgmtRaw, ok := patch["mgmt"]; ok {
		var mgmt map[string]json.RawMessage
		_ = json.Unmarshal(mgmtRaw, &mgmt)
		if !fieldPresent(mgmt, "secret") || isMaskedJSON(mgmt["secret"]) {
			merged.Mgmt.Secret = base.Mgmt.Secret
		}
	} else {
		merged.Mgmt.Secret = base.Mgmt.Secret
	}
	if clientsRaw, ok := patch["clients"]; ok {
		var patched map[string]map[string]json.RawMessage
		_ = json.Unmarshal(clientsRaw, &patched)
		for name, auth := range merged.Clients {
			baseAuth, ok := base.Clients[name]
			if !ok {
				continue
			}
			p := patched[name]
			if p == nil || !fieldPresent(p, "subscribe_token") || isMaskedJSON(p["subscribe_token"]) {
				auth.SubscribeToken = baseAuth.SubscribeToken
				merged.Clients[name] = auth
			}
		}
	} else {
		for name, auth := range merged.Clients {
			if baseAuth, ok := base.Clients[name]; ok {
				auth.SubscribeToken = baseAuth.SubscribeToken
				merged.Clients[name] = auth
			}
		}
	}
}

func fieldPresent(m map[string]json.RawMessage, key string) bool {
	if m == nil {
		return false
	}
	_, ok := m[key]
	return ok
}

func isMaskedJSON(raw json.RawMessage) bool {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return false
	}
	s = strings.TrimSpace(s)
	return s == "***" || s == "***PEM***" || strings.HasSuffix(s, "***")
}
