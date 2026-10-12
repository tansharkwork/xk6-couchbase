package xk6_couchbase

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/couchbase/gocb/v2"
	"github.com/couchbaselabs/gocbconnstr/v2"
)

// TLSConfig enables TLS and optionally authenticates with local PEM files.
// Paths are relative to the k6 process working directory, not the JS script.
// ClientCertPath and ClientKeyPath must be provided together without password credentials.
// CACertPath is optional when the server certificate is already trusted by the SDK.
type TLSConfig struct {
	CACertPath     string `json:"caCertPath,omitempty" js:"caCertPath"`
	ClientCertPath string `json:"clientCertPath,omitempty" js:"clientCertPath"`
	ClientKeyPath  string `json:"clientKeyPath,omitempty" js:"clientKeyPath"`
}

// connectionConfig is comparable so shared connections cannot silently change identity.
// It contains paths, not parsed certificate material; shared mode loads the files once.
type connectionConfig struct {
	connectionString string
	username         string
	password         string
	tls              TLSConfig
}

func normalizeConnectionConfig(dbConfig DBConfig, connectionBufferSizeBytes int) (connectionConfig, error) {
	config := connectionConfig{username: dbConfig.Username, password: dbConfig.Password}
	spec, err := gocbconnstr.Parse(dbConfig.Hostname)
	if err != nil {
		return config, fmt.Errorf("invalid couchbase connection string: %w", err)
	}
	if len(spec.Addresses) == 0 {
		return config, fmt.Errorf("couchbase hostname is required")
	}
	if spec.Scheme == "" {
		spec.Scheme = "couchbase"
		if dbConfig.TLS != nil {
			spec.Scheme = "couchbases"
		}
	}
	if spec.Scheme != "couchbase" && spec.Scheme != "couchbases" {
		return config, fmt.Errorf("connection scheme must be couchbase:// or couchbases://")
	}
	if dbConfig.TLS != nil {
		if spec.Scheme != "couchbases" {
			return config, fmt.Errorf("TLS configuration requires couchbases://; plaintext connections are not allowed")
		}
		config.tls = *dbConfig.TLS
		if (config.tls.ClientCertPath == "") != (config.tls.ClientKeyPath == "") {
			return config, fmt.Errorf("TLS clientCertPath and clientKeyPath must be provided together")
		}
		if config.tls.ClientCertPath != "" && (config.username != "" || config.password != "") {
			return config, fmt.Errorf("client certificate authentication cannot be combined with username or password")
		}
		for _, path := range []*string{&config.tls.CACertPath, &config.tls.ClientCertPath, &config.tls.ClientKeyPath} {
			if *path != "" {
				*path, err = filepath.Abs(*path)
				if err != nil {
					return config, fmt.Errorf("resolve TLS file path: %w", err)
				}
			}
		}
	}
	if config.tls.ClientCertPath == "" && (config.username == "" || config.password == "") {
		return config, fmt.Errorf("provide both username and password, or tls.clientCertPath and tls.clientKeyPath")
	}
	if spec.Options == nil {
		spec.Options = make(map[string][]string)
	}
	if connectionBufferSizeBytes > 0 {
		spec.Options["kv_buffer_size"] = []string{strconv.Itoa(connectionBufferSizeBytes)}
	} else if values := spec.Options["kv_buffer_size"]; len(values) == 0 {
		spec.Options["kv_buffer_size"] = []string{strconv.Itoa(defaultConnectionBufferSizeBytes)}
	} else if size, err := strconv.Atoi(values[len(values)-1]); err != nil || size < 1 || len(values) > 1 {
		return config, fmt.Errorf("connection string kv_buffer_size must be a single positive integer")
	}
	config.connectionString = spec.String()
	return config, nil
}

func (config connectionConfig) clusterOptions() (gocb.ClusterOptions, error) {
	var opts gocb.ClusterOptions
	if config.tls.ClientCertPath != "" {
		cert, err := tls.LoadX509KeyPair(config.tls.ClientCertPath, config.tls.ClientKeyPath)
		if err != nil {
			return opts, fmt.Errorf("load TLS client certificate and key: %w", err)
		}
		opts.Authenticator = gocb.CertificateAuthenticator{ClientCertificate: &cert}
	} else {
		opts.Authenticator = gocb.PasswordAuthenticator{Username: config.username, Password: config.password}
	}
	if config.tls.CACertPath != "" {
		pem, err := os.ReadFile(config.tls.CACertPath)
		if err != nil {
			return opts, fmt.Errorf("read TLS CA certificate: %w", err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return opts, fmt.Errorf("TLS caCertPath contains no valid PEM certificates")
		}
		opts.SecurityConfig.TLSRootCAs = roots
	}
	return opts, nil
}
