package xk6_couchbase

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTLSConfigurationErrors(t *testing.T) {
	t.Parallel()
	fixture := newTLSFixture(t)
	invalid := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(invalid, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing.pem")
	other := newTLSFixture(t)
	tests := []struct {
		name    string
		config  DBConfig
		missing bool
	}{
		{name: "missing key", config: DBConfig{Hostname: "127.0.0.1", TLS: &TLSConfig{ClientCertPath: fixture.config.ClientCertPath}}},
		{name: "missing certificate", config: DBConfig{Hostname: "127.0.0.1", TLS: &TLSConfig{ClientKeyPath: fixture.config.ClientKeyPath}}},
		{name: "plaintext TLS", config: DBConfig{Hostname: "couchbase://127.0.0.1", TLS: &fixture.config}},
		{name: "mixed username", config: DBConfig{Hostname: "127.0.0.1", Username: "u", TLS: &fixture.config}},
		{name: "mixed password", config: DBConfig{Hostname: "127.0.0.1", Password: "p", TLS: &fixture.config}},
		{name: "unsupported scheme", config: DBConfig{Hostname: "https://127.0.0.1", TLS: &fixture.config}},
		{name: "missing host", config: DBConfig{TLS: &fixture.config}},
		{name: "empty TLS authentication", config: DBConfig{Hostname: "127.0.0.1", TLS: &TLSConfig{}}},
		{name: "missing password", config: DBConfig{Hostname: "127.0.0.1", Username: "u", TLS: &TLSConfig{}}},
		{name: "missing username", config: DBConfig{Hostname: "127.0.0.1", Password: "p", TLS: &TLSConfig{}}},
		{name: "missing CA file", config: DBConfig{Hostname: "127.0.0.1", Username: "u", Password: "p", TLS: &TLSConfig{CACertPath: missing}}, missing: true},
		{name: "invalid CA PEM", config: DBConfig{Hostname: "127.0.0.1", Username: "u", Password: "p", TLS: &TLSConfig{CACertPath: invalid}}},
		{name: "missing key file", config: DBConfig{Hostname: "127.0.0.1", TLS: &TLSConfig{ClientCertPath: fixture.config.ClientCertPath, ClientKeyPath: missing}}, missing: true},
		{name: "invalid client PEM", config: DBConfig{Hostname: "127.0.0.1", TLS: &TLSConfig{ClientCertPath: invalid, ClientKeyPath: fixture.config.ClientKeyPath}}},
		{name: "mismatched key", config: DBConfig{Hostname: "127.0.0.1", TLS: &TLSConfig{ClientCertPath: fixture.config.ClientCertPath, ClientKeyPath: other.config.ClientKeyPath}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client, err := couchbaseFor(t, New()).NewClientPerVU(tt.config, nil, "1s", 0)
			if client != nil {
				_ = client.Close()
				t.Fatal("invalid TLS configuration returned a client")
			}
			if err == nil {
				t.Fatal("invalid TLS configuration was accepted")
			}
			if tt.missing && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing file error was not preserved: %v", err)
			}
		})
	}
}

func TestSharedTLSConfiguration(t *testing.T) {
	t.Parallel()
	fixture := newTLSFixture(t)
	root := New()
	cb := couchbaseFor(t, root)
	config := DBConfig{Hostname: "127.0.0.1", TLS: &fixture.config}
	first, err := cb.NewClientWithSharedConnection(config, nil, "1s", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })

	for _, changed := range []DBConfig{
		{Hostname: "127.0.0.2", TLS: &fixture.config},
		{Hostname: "127.0.0.1", Username: "u", Password: "p"},
		{Hostname: "127.0.0.1", TLS: &TLSConfig{CACertPath: fixture.config.CACertPath}},
		{Hostname: "127.0.0.1", TLS: &TLSConfig{CACertPath: "other-ca.pem", ClientCertPath: fixture.config.ClientCertPath, ClientKeyPath: fixture.config.ClientKeyPath}},
		{Hostname: "127.0.0.1", TLS: &TLSConfig{CACertPath: fixture.config.CACertPath, ClientCertPath: "other-cert.pem", ClientKeyPath: "other-key.pem"}},
	} {
		client, err := cb.NewClientWithSharedConnection(changed, nil, "1s", 0)
		if err == nil || client != nil {
			t.Fatal("a conflicting shared authentication configuration was accepted")
		}
	}

	// Shared mode must reuse the loaded identity, not reopen the files per VU.
	if err := os.Remove(fixture.config.ClientKeyPath); err != nil {
		t.Fatal(err)
	}
	equivalent := fixture.config
	// An absolute dot-segment alias stays on the same volume on Windows.
	separator := string(filepath.Separator)
	equivalent.ClientCertPath = filepath.Dir(equivalent.ClientCertPath) + separator + "." + separator + filepath.Base(equivalent.ClientCertPath)
	equivalentConfig := DBConfig{Hostname: "couchbases://127.0.0.1?kv_buffer_size=2048", TLS: &equivalent}
	var wg sync.WaitGroup
	for range 8 {
		vu := couchbaseFor(t, root)
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, err := vu.NewClientWithSharedConnection(equivalentConfig, nil, "1s", 0)
			if err != nil || client != first {
				t.Errorf("equivalent shared configuration did not reuse the initialized connection: %v", err)
			}
		}()
	}
	wg.Wait()
}

// These are real SDK-to-TLS-server handshakes, not a Couchbase protocol emulator.
// They prove certificate presentation and verification, not database authorization.
func TestSDKCertificateHandshake(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"valid identity", "untrusted server", "hostname mismatch", "untrusted client"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := newTLSFixture(t)
			serverCert := fixture.server
			clientCAs := fixture.roots
			if name == "hostname mismatch" {
				serverCert = fixture.issue(t, "wrong.example.test", x509.ExtKeyUsageServerAuth, nil)
			}
			if name == "untrusted server" {
				fixture.config.CACertPath = newTLSFixture(t).config.CACertPath
			}
			if name == "untrusted client" {
				clientCAs = newTLSFixture(t).roots
			}
			server := startTLSServer(t, serverCert, tls.RequireAndVerifyClientCert, clientCAs)
			config := DBConfig{Hostname: server.connectionString(), TLS: &fixture.config}
			client, err := couchbaseFor(t, New()).NewClientPerVU(config, nil, "1s", 0)
			if err != nil {
				t.Fatal(err)
			}
			got := server.await(t, client)
			switch name {
			case "valid identity":
				if got.err != nil || got.identity != "test-user" {
					t.Fatalf("client identity was not authenticated: identity=%q err=%v", got.identity, got.err)
				}
			case "untrusted client":
				requireRejected(t, got, serverRejectsClient)
			default:
				requireRejected(t, got, clientRejectsServer)
			}
		})
	}
}

// The handshake tests above build DBConfig in Go. These build clients from
// JavaScript, so they also prove the tls field names map onto TLSConfig.
func TestJSClientTLSHandshake(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		constructor string
		password    bool
		wrongCA     bool
	}{
		{name: "client certificate", constructor: "newClientPerVU"},
		{name: "password with custom CA", constructor: "newClientWithSharedConnection", password: true},
		{name: "password with untrusted server", constructor: "newClientPerVU", password: true, wrongCA: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fixture := newTLSFixture(t)
			if tt.wrongCA {
				fixture.config.CACertPath = newTLSFixture(t).config.CACertPath
			}
			// A password client sends no certificate. RequestClientCert asks for
			// one without requiring it, so the server can confirm none was sent.
			clientAuth := tls.RequireAndVerifyClientCert
			config := map[string]any{"tls": map[string]string{
				"caCertPath":     fixture.config.CACertPath,
				"clientCertPath": fixture.config.ClientCertPath,
				"clientKeyPath":  fixture.config.ClientKeyPath,
			}}
			if tt.password {
				clientAuth = tls.RequestClientCert
				config["username"], config["password"] = "u", "p"
				config["tls"] = map[string]string{"caCertPath": fixture.config.CACertPath}
			}
			server := startTLSServer(t, fixture.server, clientAuth, fixture.roots)
			config["hostname"] = server.connectionString()
			configJSON, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			client := clientFromModule(t, New(), "xk6_couchbase."+tt.constructor+"("+string(configJSON)+`, [], "1s")`)
			got := server.await(t, client)
			switch {
			case tt.wrongCA:
				requireRejected(t, got, clientRejectsServer)
			case got.err != nil:
				t.Fatalf("handshake failed: %v", got.err)
			case tt.password && got.identity != "":
				t.Fatalf("password client presented certificate %q", got.identity)
			case !tt.password && got.identity != "test-user":
				t.Fatalf("client identity was not authenticated: identity=%q", got.identity)
			}
		})
	}
}

type handshake struct {
	identity string
	err      error
}

// tlsServer accepts SDK connections and reports the first handshake that
// completes or fails verification.
type tlsServer struct {
	listener net.Listener
	result   chan handshake
	timer    *time.Timer
}

func startTLSServer(t *testing.T, serverCert tls.Certificate, clientAuth tls.ClientAuthType, clientCAs *x509.CertPool) *tlsServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	deadline := time.Now().Add(30 * time.Second)
	if end, ok := t.Deadline(); ok && end.Add(-time.Second).Before(deadline) {
		deadline = end.Add(-time.Second)
	}
	server := &tlsServer{listener: listener, result: make(chan handshake, 1), timer: time.NewTimer(time.Until(deadline))}
	t.Cleanup(func() { server.timer.Stop() })
	if err := listener.(*net.TCPListener).SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				server.result <- handshake{err: err}
				return
			}
			_ = conn.SetDeadline(deadline)
			tlsConn := tls.Server(conn, &tls.Config{
				Certificates: []tls.Certificate{serverCert},
				ClientAuth:   clientAuth,
				ClientCAs:    clientCAs,
				MinVersion:   tls.VersionTLS12,
			})
			err = tlsConn.Handshake()
			_ = conn.Close()
			// Bucket bootstrap can cancel an earlier cluster bootstrap socket.
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				continue
			}
			if err != nil {
				server.result <- handshake{err: err}
				return
			}
			got := handshake{}
			if peers := tlsConn.ConnectionState().PeerCertificates; len(peers) > 0 {
				got.identity = peers[0].Subject.CommonName
			}
			server.result <- got
			return
		}
	}()
	return server
}

func (server *tlsServer) connectionString() string {
	return "couchbases://" + server.listener.Addr().String() + "?enable_dns_srv=false"
}

// await starts the client's TLS bootstrap and waits for the server's verdict.
func (server *tlsServer) await(t *testing.T, client *Client) handshake {
	t.Helper()
	t.Cleanup(func() {
		// Refuse reconnects before Close waits for the SDK's dialers.
		_ = server.listener.Close()
		_ = client.Close()
	})
	// Opening a bucket starts the SDK's KV bootstrap and TLS connection.
	client.cluster.Bucket("test")
	select {
	case got := <-server.result:
		return got
	case <-server.timer.C:
		t.Fatal("SDK did not attempt a TLS handshake")
		return handshake{}
	}
}

// tlsRejection names the side of the handshake expected to reject the other.
type tlsRejection int

const (
	clientRejectsServer tlsRejection = iota
	serverRejectsClient
)

// requireRejected fails unless the expected side's certificate verification
// ended the handshake, rather than a timeout or an unrelated socket error.
func requireRejected(t *testing.T, got handshake, by tlsRejection) {
	t.Helper()
	if got.err == nil || got.identity != "" {
		t.Fatal("an invalid TLS peer completed an authenticated handshake")
	}
	switch by {
	case serverRejectsClient:
		var verifyErr *tls.CertificateVerificationError
		if !errors.As(got.err, &verifyErr) {
			t.Fatalf("server did not reject the client certificate: %v", got.err)
		}
	case clientRejectsServer:
		// The SDK rejects the server certificate by sending the server a TLS alert.
		var opErr *net.OpError
		if !errors.As(got.err, &opErr) || opErr.Op != "remote error" {
			t.Fatalf("client did not reject the server certificate with a TLS alert: %v", got.err)
		}
	}
}

type tlsFixture struct {
	config TLSConfig
	roots  *x509.CertPool
	server tls.Certificate
	ca     *x509.Certificate
	key    *ecdsa.PrivateKey
}

func newTLSFixture(t *testing.T) *tlsFixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	fixture := &tlsFixture{ca: ca, key: key, roots: x509.NewCertPool()}
	fixture.roots.AppendCertsFromPEM(caPEM)
	fixture.server = fixture.issue(t, "test-server", x509.ExtKeyUsageServerAuth, net.ParseIP("127.0.0.1"))
	client := fixture.issue(t, "test-user", x509.ExtKeyUsageClientAuth, nil)
	keyDER, err := x509.MarshalPKCS8PrivateKey(client.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	fixture.config = TLSConfig{
		CACertPath: filepath.Join(dir, "ca.pem"), ClientCertPath: filepath.Join(dir, "client.pem"), ClientKeyPath: filepath.Join(dir, "client-key.pem"),
	}
	for path, content := range map[string][]byte{
		fixture.config.CACertPath:     caPEM,
		fixture.config.ClientCertPath: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: client.Certificate[0]}),
		fixture.config.ClientKeyPath:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	} {
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func (fixture *tlsFixture) issue(t *testing.T, name string, usage x509.ExtKeyUsage, ip net.IP) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
	}
	if ip != nil {
		cert.IPAddresses = []net.IP{ip}
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, fixture.ca, &key.PublicKey, fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
