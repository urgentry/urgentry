package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
)

const (
	totpDigits   = 6
	totpPeriod   = 30
	totpSkew     = 1 // allow +/- 1 step for clock skew
	secretLength = 20
	recoveryLen  = 8
	recoveryCnt  = 10
)

// TOTPSecret generates a new base32-encoded TOTP secret.
func TOTPSecret() (string, error) {
	secret := make([]byte, secretLength)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("generate TOTP secret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret), nil
}

// TOTPURI returns the otpauth:// URI suitable for QR code rendering.
// issuer is the service name (e.g. "Urgentry"), account is typically the
// user's email address.
func TOTPURI(secret, issuer, account string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", fmt.Sprintf("%d", totpDigits))
	v.Set("period", fmt.Sprintf("%d", totpPeriod))

	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	return fmt.Sprintf("otpauth://totp/%s?%s", label, v.Encode())
}

// VerifyTOTP checks whether a 6-digit code is valid for the given secret
// at the current time. It accepts codes within +/- totpSkew periods to
// tolerate modest clock drift.
func VerifyTOTP(secret, code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return false
	}
	now := time.Now().UTC().Unix()
	counter := now / totpPeriod

	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(
		strings.ToUpper(strings.TrimSpace(secret)),
	)
	if err != nil {
		return false
	}

	for i := -totpSkew; i <= totpSkew; i++ {
		expected := generateCode(key, counter+int64(i))
		if hmac.Equal([]byte(expected), []byte(code)) {
			return true
		}
	}
	return false
}

// generateCode implements HOTP (RFC 4226) for a single counter value.
func generateCode(key []byte, counter int64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(counter))

	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	otp := code % uint32(math.Pow10(totpDigits))

	return fmt.Sprintf("%06d", otp)
}

// GenerateRecoveryCodes produces a set of single-use recovery codes.
// Each code is a dash-separated pair of 4 hex characters for readability.
func GenerateRecoveryCodes() ([]string, error) {
	codes := make([]string, recoveryCnt)
	for i := range codes {
		b := make([]byte, recoveryLen)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("generate recovery code: %w", err)
		}
		hex := fmt.Sprintf("%x", b)
		codes[i] = hex[:4] + "-" + hex[4:8] + "-" + hex[8:12] + "-" + hex[12:16]
	}
	return codes, nil
}

// TOTPEnrollment holds the data needed to present a TOTP enrollment UI.
type TOTPEnrollment struct {
	Secret        string   `json:"secret"`
	URI           string   `json:"uri"`
	RecoveryCodes []string `json:"recovery_codes"`
}

// NewTOTPEnrollment creates a complete enrollment bundle for a user.
func NewTOTPEnrollment(issuer, account string) (*TOTPEnrollment, error) {
	secret, err := TOTPSecret()
	if err != nil {
		return nil, err
	}
	codes, err := GenerateRecoveryCodes()
	if err != nil {
		return nil, err
	}
	return &TOTPEnrollment{
		Secret:        secret,
		URI:           TOTPURI(secret, issuer, account),
		RecoveryCodes: codes,
	}, nil
}

// TOTPUserStore extends user management with TOTP persistence.
type TOTPUserStore interface {
	// SetTOTPSecret stores the TOTP secret and recovery codes for a user,
	// enabling 2FA on their account.
	SetTOTPSecret(ctx context.Context, userID, secret string, recoveryCodes []string) error

	// GetTOTPSecret returns the TOTP secret for a user. Returns empty string
	// if TOTP is not enabled.
	GetTOTPSecret(ctx context.Context, userID string) (string, error)

	// IsTOTPEnabled reports whether the user has TOTP 2FA enabled.
	IsTOTPEnabled(ctx context.Context, userID string) (bool, error)

	// DisableTOTP removes the TOTP secret and recovery codes for a user.
	DisableTOTP(ctx context.Context, userID string) error

	// UseRecoveryCode marks a recovery code as used and returns true if the
	// code was valid and previously unused.
	UseRecoveryCode(ctx context.Context, userID, code string) (bool, error)
}
