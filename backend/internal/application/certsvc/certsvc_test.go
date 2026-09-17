package certsvc_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gateway-center/backend/internal/application/certsvc"
)

func generateTestCert(t *testing.T, cn string, dns []string, notAfter time.Time) (string, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(100),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-24 * time.Hour),
		NotAfter:     notAfter,
		DNSNames:     dns,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})

	return string(certPEM), string(keyPEM)
}

func TestCertSvc_Parse_Success(t *testing.T) {
	expiry := time.Now().AddDate(0, 3, 0)
	certPEM, keyPEM := generateTestCert(t, "example.com", []string{"example.com", "*.example.com"}, expiry)

	svc := certsvc.New(nil, nil, nil, nil)
	res, apiErr := svc.Parse(certsvc.ParseInput{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	require.Nil(t, apiErr)
	require.NotNil(t, res)
	assert.True(t, res.Valid)
	assert.True(t, res.KeyMatches)
	assert.Equal(t, "example.com", res.Subject)
	assert.Contains(t, res.Sans, "*.example.com")
	assert.Greater(t, res.DaysRemaining, 80)
}

func TestCertSvc_Parse_MismatchedKey(t *testing.T) {
	expiry := time.Now().AddDate(0, 1, 0)
	certPEM, _ := generateTestCert(t, "example.com", nil, expiry)
	_, otherKeyPEM := generateTestCert(t, "other.com", nil, expiry)

	svc := certsvc.New(nil, nil, nil, nil)
	res, apiErr := svc.Parse(certsvc.ParseInput{
		CertPEM: certPEM,
		KeyPEM:  otherKeyPEM,
	})

	require.Nil(t, apiErr)
	require.NotNil(t, res)
	assert.True(t, res.Valid)
	assert.False(t, res.KeyMatches)
	assert.Contains(t, res.ErrorMessage, "不匹配")
}

func TestCertSvc_Parse_InvalidPEM(t *testing.T) {
	svc := certsvc.New(nil, nil, nil, nil)
	res, apiErr := svc.Parse(certsvc.ParseInput{
		CertPEM: "not-a-valid-pem",
	})
	require.Nil(t, apiErr)
	assert.False(t, res.Valid)
}
