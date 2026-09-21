package doubao

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

// Authentication is read from the selected desktop profile for each operation.
// Neither the key nor decrypted cookies are persisted or passed in process args.
func desktopAuth(ctx context.Context, profile string) (http.Header, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("doubao desktop authentication currently requires macOS")
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(profile, "Cookies"), RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open Doubao cookies: %w", err)
	}
	defer func() { _ = db.Close() }()
	var version int
	if err := db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = 'version'").Scan(&version); err != nil {
		return nil, fmt.Errorf("read Doubao cookie schema: %w", err)
	}
	password, err := exec.CommandContext(ctx, "security", "find-generic-password", "-s", "Doubao Safe Storage", "-w").Output()
	if err != nil {
		return nil, fmt.Errorf("cannot read Doubao Safe Storage from macOS Keychain; allow access and sign in to Doubao")
	}
	key, err := pbkdf2.Key(sha1.New, strings.TrimRight(string(password), "\r\n"), []byte("saltysalt"), 1003, 16)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT host_key, name, value, encrypted_value, expires_utc
FROM cookies WHERE host_key IN ('.doubao.com', 'www.doubao.com') AND path = '/'
AND name IN ('sessionid', 'sid_guard', 'passport_csrf_token', 'ttwid') ORDER BY host_key`)
	if err != nil {
		return nil, fmt.Errorf("read Doubao cookies: %w", err)
	}
	defer func() { _ = rows.Close() }()
	cookies := map[string]string{}
	for rows.Next() {
		var host, name, value string
		var encrypted []byte
		var expires int64
		if err := rows.Scan(&host, &name, &value, &encrypted, &expires); err != nil {
			return nil, err
		}
		if expires > 0 && expires/1_000_000-11644473600 <= time.Now().Unix() {
			continue
		}
		if len(encrypted) > 0 {
			value, err = decryptCookie(encrypted, key, host, version)
			if err != nil {
				return nil, fmt.Errorf("decrypt Doubao %s cookie: %w", name, err)
			}
		}
		if value != "" {
			cookies[name] = value
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cookieHeaders(cookies)
}

func decryptCookie(encrypted, key []byte, host string, version int) (string, error) {
	if len(encrypted) < 3 || string(encrypted[:3]) != "v10" {
		return "", fmt.Errorf("unsupported cookie encryption")
	}
	ct := encrypted[3:]
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return "", fmt.Errorf("invalid encrypted cookie length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	plain := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, bytes.Repeat([]byte{' '}, aes.BlockSize)).CryptBlocks(plain, ct)
	pad := int(plain[len(plain)-1])
	if pad < 1 || pad > aes.BlockSize || !bytes.Equal(plain[len(plain)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
		return "", fmt.Errorf("invalid cookie padding")
	}
	plain = plain[:len(plain)-pad]
	if version >= 24 {
		digest := sha256.Sum256([]byte(host))
		if len(plain) < len(digest) || !bytes.Equal(plain[:len(digest)], digest[:]) {
			return "", fmt.Errorf("cookie host digest mismatch")
		}
		plain = plain[len(digest):]
	}
	if !utf8.Valid(plain) {
		return "", fmt.Errorf("invalid cookie encoding")
	}
	return string(plain), nil
}

func cookieHeaders(cookies map[string]string) (http.Header, error) {
	if cookies["sessionid"] == "" {
		return nil, fmt.Errorf("doubao login is missing or expired; sign in to the desktop app")
	}
	var parts []string
	for _, name := range []string{"sessionid", "sid_guard", "passport_csrf_token", "ttwid"} {
		value := cookies[name]
		if value == "" {
			continue
		}
		for _, c := range []byte(value) {
			if c < 0x21 || c > 0x7e || c == ';' || c == '"' || c == '\\' {
				return nil, fmt.Errorf("invalid Doubao %s cookie", name)
			}
		}
		parts = append(parts, name+"="+value)
	}
	h := make(http.Header)
	h.Set("Cookie", strings.Join(parts, "; "))
	h.Set("x-tt-passport-csrf-token", cookies["passport_csrf_token"])
	return h, nil
}
