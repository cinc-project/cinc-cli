package client

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cinc-project/cinc-cli/cli/config"
)

// defaultHTTPTimeout mirrors cinc-api's own default client timeout. Trusting
// extra certificates means handing cinc-api a custom *http.Client, which
// replaces its default one wholesale, so the timeout has to be carried over
// here or requests against a hung server would wait forever.
const defaultHTTPTimeout = 30 * time.Second

// TrustedCerts is the result of loading a trusted_certs_dir: the cert pool to
// verify servers against, plus which files contributed to it.
type TrustedCerts struct {
	// Dir is the directory the certificates were read from.
	Dir string
	// Pool is a copy of the system pool with every loaded certificate added.
	Pool *x509.CertPool
	// Loaded names the files (base names, sorted) that held at least one
	// certificate.
	Loaded []string
	// Skipped names the *.crt / *.pem files that held no parseable
	// certificate or could not be read. Normal commands ignore them;
	// `cinc config validate` reports them.
	Skipped []string
}

// isCertFileName reports whether name has one of the extensions knife reads
// from its trusted_certs_dir (*.crt and *.pem), ignoring case.
func isCertFileName(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".crt" || ext == ".pem"
}

// LoadTrustedCerts reads every *.crt and *.pem file in dir and adds the
// certificates they hold to a copy of the system cert pool (an empty pool when
// the system one is unavailable). Subdirectories and other files are ignored;
// a certificate file that holds nothing parseable is listed in Skipped rather
// than failing the load. The returned errors deliberately do not wrap
// fs.ErrNotExist or fs.ErrPermission, so the command layer never mistakes
// them for a missing client key.
func LoadTrustedCerts(dir string) (*TrustedCerts, error) {
	info, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		return nil, fmt.Errorf("we couldn't find the trusted_certs_dir %s set in your profile. Create it and put your CA certificates (*.crt or *.pem) in it, or remove trusted_certs_dir to trust only the system certificates.", dir)
	case err != nil:
		return nil, fmt.Errorf("we couldn't read the trusted_certs_dir %s: %v. Check the directory's permissions, since cinc reads it to decide which servers to trust.", dir, err)
	case !info.IsDir():
		return nil, fmt.Errorf("trusted_certs_dir %s is a file, not a directory. Point it at the directory that holds your CA certificates (*.crt or *.pem).", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("we couldn't read the trusted_certs_dir %s: %v. Check the directory's permissions, since cinc reads it to decide which servers to trust.", dir, err)
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	tc := &TrustedCerts{Dir: dir, Pool: pool}
	for _, entry := range entries {
		if entry.IsDir() || !isCertFileName(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil || !pool.AppendCertsFromPEM(data) {
			tc.Skipped = append(tc.Skipped, entry.Name())
			continue
		}
		tc.Loaded = append(tc.Loaded, entry.Name())
	}
	sort.Strings(tc.Loaded)
	sort.Strings(tc.Skipped)
	return tc, nil
}

// TrustedCertsFor resolves and loads the profile's trusted certificates. It
// returns nil, nil when the profile trusts only the system pool: no
// trusted_certs_dir is configured and neither default directory exists. An
// explicitly configured directory that is missing or unreadable is an error.
func TrustedCertsFor(p config.Profile) (*TrustedCerts, error) {
	dir, _, err := p.ResolveTrustedCertsDir()
	if err != nil {
		return nil, err
	}
	if dir == "" {
		return nil, nil
	}
	return LoadTrustedCerts(dir)
}

// trustedHTTPClient builds the *http.Client handed to cinc-api when extra
// certificates are trusted. It clones http.DefaultTransport, so proxy support,
// HTTP/2 and connection pooling match what cinc-api would otherwise use, and
// sets only RootCAs. cinc-api still applies ssl_verify_mode on top of it.
func trustedHTTPClient(pool *x509.CertPool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{RootCAs: pool}
	return &http.Client{Timeout: defaultHTTPTimeout, Transport: tr}
}
