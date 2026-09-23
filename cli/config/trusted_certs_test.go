package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReadsTrustedCertsDir(t *testing.T) {
	path := writeConfig(t, `
[default]
client_name       = "tim"
client_key        = "/keys/tim.pem"
cinc_server_url   = "https://cinc.example.com/organizations/acme"
trusted_certs_dir = "/etc/cinc/trusted_certs"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Profiles["default"].TrustedCertsDir; got != "/etc/cinc/trusted_certs" {
		t.Errorf("TrustedCertsDir = %q, want /etc/cinc/trusted_certs", got)
	}
}

func TestUpdateProfilePreservesTrustedCertsDir(t *testing.T) {
	path := writeConfig(t, `
[default]
client_name       = "tim"
client_key        = "/keys/tim.pem"
cinc_server_url   = "https://cinc.example.com/organizations/acme"
trusted_certs_dir = "~/.chef/trusted_certs"
`)
	if err := UpdateProfile(path, "default", func(p *Profile) error {
		p.ClientName = "sam"
		return nil
	}); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Written back verbatim: the ~ is not expanded into the file.
	if got := cfg.Profiles["default"].TrustedCertsDir; got != "~/.chef/trusted_certs" {
		t.Errorf("TrustedCertsDir = %q, want it preserved as ~/.chef/trusted_certs", got)
	}
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestResolveTrustedCertsDir(t *testing.T) {
	tests := []struct {
		name          string
		configured    string
		makeDirs      []string // relative to HOME
		want          string   // relative to HOME when wantUnderHome
		wantUnderHome bool
		wantExplicit  bool
	}{
		{name: "nothing configured and no default dirs", want: ""},
		{name: "cinc default", makeDirs: []string{".cinc/trusted_certs"}, want: ".cinc/trusted_certs", wantUnderHome: true},
		{name: "chef default as a fallback", makeDirs: []string{".chef/trusted_certs"}, want: ".chef/trusted_certs", wantUnderHome: true},
		{name: "cinc default wins over chef", makeDirs: []string{".cinc/trusted_certs", ".chef/trusted_certs"}, want: ".cinc/trusted_certs", wantUnderHome: true},
		{name: "explicit absolute", configured: "/etc/cinc/certs", makeDirs: []string{".cinc/trusted_certs"}, want: "/etc/cinc/certs", wantExplicit: true},
		{name: "explicit with tilde", configured: "~/certs", want: "certs", wantUnderHome: true, wantExplicit: true},
		{name: "explicit relative stays relative", configured: "certs", want: "certs", wantExplicit: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			for _, d := range tt.makeDirs {
				mkdirAll(t, filepath.Join(home, d))
			}
			dir, explicit, err := Profile{TrustedCertsDir: tt.configured}.ResolveTrustedCertsDir()
			if err != nil {
				t.Fatalf("ResolveTrustedCertsDir: %v", err)
			}
			want := tt.want
			if tt.wantUnderHome {
				want = filepath.Join(home, tt.want)
			}
			if dir != want {
				t.Errorf("dir = %q, want %q", dir, want)
			}
			if explicit != tt.wantExplicit {
				t.Errorf("explicit = %v, want %v", explicit, tt.wantExplicit)
			}
		})
	}
}

func TestResolveTrustedCertsDirIgnoresDefaultThatIsAFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mkdirAll(t, filepath.Join(home, ".cinc"))
	if err := os.WriteFile(filepath.Join(home, ".cinc", "trusted_certs"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	mkdirAll(t, filepath.Join(home, ".chef", "trusted_certs"))
	dir, _, err := Profile{}.ResolveTrustedCertsDir()
	if err != nil {
		t.Fatalf("ResolveTrustedCertsDir: %v", err)
	}
	if want := filepath.Join(home, ".chef", "trusted_certs"); dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
}
