package generate_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"gateway-center/backend/internal/generate"
)

func TestGenerateWithCertificatesProducesTLSConfig(t *testing.T) {
	snap := &generate.Snapshot{
		NodeID:   "n1",
		NodeName: "edge-1",
		EnvType:  "test",
		Services: []generate.ServiceSnap{{
			Name: "crm", Targets: []generate.TargetSnap{{URL: "http://10.0.0.1:80", Weight: 1}},
		}},
		Routers: []generate.RouterSnap{{
			Name: "crm", Mode: "simple", DomainName: "crm.example.com", Path: "/",
			MatchType: "prefix", ServiceName: "crm", EntryPoint: "websecure",
			HTTPS: true, CertMode: "imported", Priority: 10,
		}},
		Certificates: []generate.CertSnap{{
			ID:            "cert-uuid-1",
			Name:          "crm-cert",
			DomainName:    "crm.example.com",
			CertPEM:       "-----BEGIN CERTIFICATE-----\nMIIB...fake...\n-----END CERTIFICATE-----",
			PrivateKeyRef: "cert-uuid-1",
		}},
	}

	arts, err := generate.Generate(snap)
	require.NoError(t, err)

	paths := make(map[string]string)
	for _, a := range arts {
		paths[a.Path] = string(a.Content)
	}

	assert.Contains(t, paths, "tls/crm-cert.pem")
	assert.Contains(t, paths, "tls/crm-cert.key")
	assert.Contains(t, paths, "tls/certificates.yml")

	var tlsDoc struct {
		TLS struct {
			Certificates []struct {
				CertFile string `yaml:"certFile"`
				KeyFile  string `yaml:"keyFile"`
			} `yaml:"certificates"`
		} `yaml:"tls"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(paths["tls/certificates.yml"]), &tlsDoc))
	require.Len(t, tlsDoc.TLS.Certificates, 1)
	assert.Equal(t, "/etc/traefik/dynamic/tls/crm-cert.pem", tlsDoc.TLS.Certificates[0].CertFile)
	assert.Equal(t, "/etc/traefik/dynamic/tls/crm-cert.key", tlsDoc.TLS.Certificates[0].KeyFile)
}
