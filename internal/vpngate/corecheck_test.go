package vpngate

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This optional integration check only parses/constructs configurations; it never
// invokes `run`, starts a VPN, changes Windows routes, or installs a service.
func TestOfficialCoreCheck(t *testing.T) {
	core := os.Getenv("VPNGATE_CHECK_CORE")
	if core == "" {
		t.Skip("set VPNGATE_CHECK_CORE to sing-box 1.14.4 executable")
	}
	rulesDir := os.Getenv("VPNGATE_CHECK_RULES")
	if rulesDir == "" {
		t.Fatal("VPNGATE_CHECK_RULES directory required")
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "VPNGate local config fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	privateKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	config := strings.ReplaceAll(ovpnFixture, "-----BEGIN CERTIFICATE-----\nfixture-ca\n-----END CERTIFICATE-----\n", certificate)
	config = strings.ReplaceAll(config, "-----BEGIN CERTIFICATE-----\nfixture-client\n-----END CERTIFICATE-----\n", certificate)
	config = strings.ReplaceAll(config, "-----BEGIN PRIVATE KEY-----\nfixture-key\n-----END PRIVATE KEY-----\n", privateKey)
	node := Node{Config: config}
	for _, preset := range []string{"direct", "bypass-cn", "blacklist", "global"} {
		for _, offline := range []bool{false, true} {
			name := preset
			if offline {
				name += "-offline"
			}
			t.Run(name, func(t *testing.T) {
				s := configuredSettings()
				s.RoutingPreset = preset
				n := &node
				if offline {
					n = nil
				}
				raw, err := BuildConfig(s, n, ConfigOptions{Production: true, ControllerPort: 20123, CNRulesPath: filepath.Join(rulesDir, "cn.srs"), GFWRulesPath: filepath.Join(rulesDir, "gfw.srs")})
				if err != nil {
					t.Fatal(err)
				}
				checkCoreConfig(t, core, raw)
			})
		}
	}
	t.Run("remote-upstream-with-tun", func(t *testing.T) {
		s := configuredSettings()
		s.Upstream.Host = "proxy.example.org"
		s.TUNEnabled = true
		s.RoutingPreset = "global"
		raw, err := BuildConfig(s, &node, ConfigOptions{Production: true, ControllerPort: 20123})
		if err != nil {
			t.Fatal(err)
		}
		checkCoreConfig(t, core, raw)
	})
	t.Run("probe", func(t *testing.T) {
		raw, err := BuildConfig(configuredSettings(), &node, ConfigOptions{MixedPort: 24133})
		if err != nil {
			t.Fatal(err)
		}
		checkCoreConfig(t, core, raw)
	})
}
func checkCoreConfig(t *testing.T, core string, raw []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "check.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(core, "check", "-c", path).CombinedOutput()
	if err != nil {
		t.Fatalf("core check failed: %v\n%s", err, out)
	}
}
