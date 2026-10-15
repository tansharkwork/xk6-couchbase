package xk6_couchbase

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/couchbase/gocb/v2"
	"go.k6.io/k6/v2/js/modulestest"
)

func TestParseStringToDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  time.Duration
	}{
		{name: "seconds", input: "5s", want: 5 * time.Second},
		{name: "milliseconds", input: "250ms", want: 250 * time.Millisecond},
		{name: "compound", input: "1m30s", want: 90 * time.Second},
		{name: "empty falls back to default", input: "", want: defaultBucketReadinessTimeout},
		{name: "garbage falls back to default", input: "not-a-duration", want: defaultBucketReadinessTimeout},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parseStringToDuration(tt.input); got != tt.want {
				t.Fatalf("parseStringToDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// Module instances from one k6 run share a client; independent runs must not.
func TestSharedConnectionSingleton(t *testing.T) {
	t.Parallel()

	root := New()
	expression := `xk6_couchbase.newClientWithSharedConnection({hostname: "127.0.0.1", username: "u", password: "p"}, [], "1s")`
	client1 := clientFromModule(t, root, expression)
	t.Cleanup(func() { _ = client1.Close() })
	client2 := clientFromModule(t, root, expression)
	if client1 != client2 {
		t.Fatal("shared mode returned different clients across VUs")
	}
	otherRun := clientFromModule(t, New(), expression)
	t.Cleanup(func() { _ = otherRun.Close() })
	if otherRun == client1 {
		t.Fatal("independent module roots shared a connection")
	}

	cbVU2 := couchbaseFor(t, root)
	changed := DBConfig{Hostname: "127.0.0.1", Username: "u", Password: "different"}
	if client, err := cbVU2.NewClientWithSharedConnection(changed, nil, "1s", 0); err == nil || client != nil {
		t.Fatal("shared mode accepted different password credentials")
	}
}

// Dedicated constructors must neither reuse clients nor seed the shared singleton.
func TestPerVUConnection(t *testing.T) {
	t.Parallel()

	root := New()
	expression := `xk6_couchbase.newClientPerVU({hostname: "127.0.0.1", username: "dedicated", password: "p"}, [], "1s")`
	client1 := clientFromModule(t, root, expression)
	t.Cleanup(func() { _ = client1.Close() })
	client2 := clientFromModule(t, root, expression)
	t.Cleanup(func() { _ = client2.Close() })
	if client1 == client2 {
		t.Fatal("per-VU mode reused a client")
	}

	shared := clientFromModule(t, root, `xk6_couchbase.newClientWithSharedConnection({hostname: "127.0.0.1", username: "shared", password: "p"}, [], "1s")`)
	t.Cleanup(func() { _ = shared.Close() })
	if shared == client1 || shared == client2 {
		t.Fatal("dedicated mode populated the shared connection")
	}
}

// couchbaseFor spins up a real VU-scoped CouchBase export from the given root,
// mirroring how k6 hands the API object to each VU.
func couchbaseFor(t *testing.T, root *RootModule) *CouchBase {
	t.Helper()
	rt := modulestest.NewRuntime(t)
	inst := root.NewModuleInstance(rt.VU)
	cb, ok := inst.Exports().Default.(*CouchBase)
	if !ok {
		t.Fatalf("Default export is %T, want *CouchBase", inst.Exports().Default)
	}
	return cb
}

func clientFromModule(t *testing.T, root *RootModule, expression string) *Client {
	t.Helper()
	rt := modulestest.NewRuntime(t)
	if err := rt.SetupModuleSystem(map[string]any{"k6/x/couchbase": root}, nil, nil); err != nil {
		t.Fatal(err)
	}
	value, err := rt.RunOnEventLoop(`const xk6_couchbase = require("k6/x/couchbase"); ` + expression)
	if err != nil {
		t.Fatal(err)
	}
	client, ok := value.Export().(*Client)
	if !ok {
		t.Fatalf("constructor returned %T, want a Client", value.Export())
	}
	return client
}

func TestSharedConnectionBufferConfiguration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		host              string
		buffer            int
		equivalentHost    string
		equivalentBuffer  int
		conflictingBuffer int
	}{
		{name: "constructor overrides URI", host: "127.0.0.1?kv_buffer_size=8192", buffer: 4096, equivalentHost: "127.0.0.1", equivalentBuffer: 4096, conflictingBuffer: 8192},
		{name: "URI when constructor is omitted", host: "127.0.0.1?kv_buffer_size=4096", equivalentHost: "127.0.0.1", equivalentBuffer: 4096, conflictingBuffer: 8192},
		{name: "URI value with a leading zero", host: "127.0.0.1?kv_buffer_size=04096", equivalentHost: "127.0.0.1", equivalentBuffer: 4096, conflictingBuffer: 8192},
		{name: "nonpositive values use the default", host: "127.0.0.1", equivalentHost: "127.0.0.1", equivalentBuffer: -1, conflictingBuffer: 2 * defaultConnectionBufferSizeBytes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cb := couchbaseFor(t, New())
			config := DBConfig{Hostname: tt.host, Username: "u", Password: "p"}
			first, err := cb.NewClientWithOptions(config, options{ConnectionBufferSizeBytes: tt.buffer})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = first.Close() })
			config.Hostname = tt.equivalentHost
			reused, err := cb.NewClientWithOptions(config, options{ConnectionBufferSizeBytes: tt.equivalentBuffer})
			if err != nil || reused != first {
				t.Fatalf("equivalent buffer settings did not reuse the shared client: %v", err)
			}
			if client, err := cb.NewClientWithOptions(config, options{ConnectionBufferSizeBytes: tt.conflictingBuffer}); err == nil || client != nil {
				t.Fatal("different buffer sizes silently reused a shared client")
			}
		})
	}
}

func TestInvalidURIBufferSize(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"kv_buffer_size=abc", "kv_buffer_size=0", "kv_buffer_size=-1", "kv_buffer_size=1&kv_buffer_size=2"} {
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			config := DBConfig{Hostname: "127.0.0.1?" + query, Username: "u", Password: "p"}
			if client, err := couchbaseFor(t, New()).NewClientPerVU(config, nil, "1s", 0); err == nil || client != nil {
				t.Fatal("invalid URI kv_buffer_size was accepted")
			}
			// A positive constructor value replaces the URI value entirely.
			client, err := couchbaseFor(t, New()).NewClientPerVU(config, nil, "1s", 4096)
			if err != nil {
				t.Fatalf("constructor buffer size did not override the URI value: %v", err)
			}
			_ = client.Close()
		})
	}
}

func TestSharedInitializationFailure(t *testing.T) {
	t.Parallel()
	cb := couchbaseFor(t, New())
	config := DBConfig{
		Hostname: "127.0.0.1", Username: "u", Password: "p",
		TLS: &TLSConfig{CACertPath: filepath.Join(t.TempDir(), "missing-ca.pem")},
	}
	if client, err := cb.NewClientWithSharedConnection(config, nil, "1s", 0); client != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected the initial missing-file failure, got client=%v err=%v", client, err)
	}
	changed := DBConfig{Hostname: "127.0.0.2", Username: "other", Password: "p"}
	if client, err := cb.NewClientWithSharedConnection(changed, nil, "1s", 0); client != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shared initialization failure was replaced by a configuration conflict: %v", err)
	}
}

func TestTLSMisspelledFieldsWithoutCredentials(t *testing.T) {
	t.Parallel()
	rt := modulestest.NewRuntime(t)
	if err := rt.SetupModuleSystem(map[string]any{"k6/x/couchbase": New()}, nil, nil); err != nil {
		t.Fatal(err)
	}
	_, err := rt.RunOnEventLoop(`
		const xk6_couchbase = require("k6/x/couchbase");
		const newClientPerVU = xk6_couchbase.newClientPerVU.bind(xk6_couchbase);
		for (const tls of [{clientCert: "unused.pem"}, {ca_cert_path: "unused.pem"}]) {
			let rejected = false;
			try {
				const client = newClientPerVU({hostname: "127.0.0.1", tls}, [], "1s");
				client.close();
			} catch (error) {
				if (!String(error).includes("provide both username and password")) throw error;
				rejected = true;
			}
			if (!rejected) throw new Error("TLS configuration without authentication was accepted");
		}
	`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRemoveTimeout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  time.Duration
		err   bool
	}{
		{input: "", want: defaultRemoveTimeout},
		{input: "250ms", want: 250 * time.Millisecond},
		{input: "5s", want: 5 * time.Second},
		{input: "0s", err: true},
		{input: "-1s", err: true},
		{input: "soon", err: true},
	}
	for _, tt := range tests {
		got, err := RemoveOptions{Timeout: tt.input}.timeout()
		if (err != nil) != tt.err || got != tt.want {
			t.Errorf("timeout(%q) = %v, %v; want %v, error %v", tt.input, got, err, tt.want, tt.err)
		}
	}
}

// The trailing remove argument is optional and must tolerate the stray document
// argument older README examples passed. Valid calls get past option parsing and
// fail only at bucket readiness, because no cluster is running.
func TestRemoveArgumentsFromJS(t *testing.T) {
	t.Parallel()
	rt := modulestest.NewRuntime(t)
	if err := rt.SetupModuleSystem(map[string]any{"k6/x/couchbase": New()}, nil, nil); err != nil {
		t.Fatal(err)
	}
	_, err := rt.RunOnEventLoop(`
		const xk6_couchbase = require("k6/x/couchbase");
		const client = xk6_couchbase.newClientPerVU({hostname: "127.0.0.1", username: "u", password: "p"}, [], "10ms");
		const outcome = (call) => { try { call(); return "ok"; } catch (error) { return String(error); } };
		const accepted = {
			"no options": () => client.remove("b", "s", "c", "id"),
			"stray document": () => client.remove("b", "s", "c", "id", {title: "doc"}),
			"timeout": () => client.remove("b", "s", "c", "id", {timeout: "2s"}),
			"durability default": () => client.removeWithDurability("b", "s", "c", "id"),
			"durability timeout": () => client.removeWithDurability("b", "s", "c", "id", {timeout: "5s"}),
		};
		for (const [name, call] of Object.entries(accepted)) {
			const result = outcome(call);
			if (!result.includes("bucket connection for remove")) throw new Error(name + ": " + result);
		}
		for (const call of [
			() => client.remove("b", "s", "c", "id", {timeout: "soon"}),
			() => client.removeWithDurability("b", "s", "c", "id", {timeout: "0s"}),
		]) {
			const result = outcome(call);
			if (!result.includes("remove timeout must be a positive duration")) throw new Error(result);
		}
		client.close();
	`)
	if err != nil {
		t.Fatal(err)
	}
}

// Without a cluster, exists must surface the bucket failure as a thrown error
// rather than report the document as missing.
func TestExistsThrowsOnOperationFailure(t *testing.T) {
	t.Parallel()
	rt := modulestest.NewRuntime(t)
	if err := rt.SetupModuleSystem(map[string]any{"k6/x/couchbase": New()}, nil, nil); err != nil {
		t.Fatal(err)
	}
	_, err := rt.RunOnEventLoop(`
		const xk6_couchbase = require("k6/x/couchbase");
		const client = xk6_couchbase.newClientPerVU({hostname: "127.0.0.1", username: "u", password: "p"}, [], "10ms");
		let result;
		try {
			result = client.exists("b", "s", "c", "id");
		} catch (error) {
			result = String(error);
		}
		client.close();
		if (typeof result !== "string" || !result.includes("bucket connection for exists")) {
			throw new Error("exists did not throw on bucket failure: " + result);
		}
	`)
	if err != nil {
		t.Fatal(err)
	}
}

// fakeRows replays rows and then reports a terminal error, like a query that
// streams results before the server aborts it.
type fakeRows struct {
	rows     []any
	next     int
	rowErr   error
	err      error
	closeErr error
	closed   bool
}

func (f *fakeRows) Next() bool {
	if f.next >= len(f.rows) {
		return false
	}
	f.next++
	return true
}

func (f *fakeRows) Row(valuePtr interface{}) error {
	if f.rowErr != nil {
		return f.rowErr
	}
	*valuePtr.(*any) = f.rows[f.next-1]
	return nil
}

func (f *fakeRows) Err() error { return f.err }

func (f *fakeRows) Close() error {
	f.closed = true
	return f.closeErr
}

func TestLastRow(t *testing.T) {
	t.Parallel()
	streamErr := errors.New("abort after rows")
	tests := []struct {
		name    string
		rows    fakeRows
		want    any
		wantErr error
	}{
		{name: "last row", rows: fakeRows{rows: []any{1, 2, 3}}, want: 3},
		{name: "no rows", rows: fakeRows{}},
		{name: "error after rows", rows: fakeRows{rows: []any{1, 2}, err: streamErr}, wantErr: streamErr},
		{name: "row decode error", rows: fakeRows{rows: []any{1}, rowErr: streamErr}, wantErr: streamErr},
		{name: "close error", rows: fakeRows{rows: []any{1}, closeErr: streamErr}, wantErr: streamErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := lastRow(&tt.rows)
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("lastRow error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil && got != nil {
				t.Fatalf("lastRow returned row %v alongside error %v", got, err)
			}
			if tt.wantErr == nil && got != tt.want {
				t.Fatalf("lastRow = %v, want %v", got, tt.want)
			}
			if !tt.rows.closed {
				t.Fatal("lastRow did not close the result")
			}
		})
	}
}

func TestInsertBatchErr(t *testing.T) {
	t.Parallel()
	ops := func(failed int, total int) []gocb.BulkOp {
		batch := make([]gocb.BulkOp, total)
		for i := range batch {
			op := &gocb.InsertOp{ID: fmt.Sprintf("doc-%d", i)}
			if i < failed {
				op.Err = gocb.ErrDocumentExists
			}
			batch[i] = op
		}
		return batch
	}

	if err := insertBatchErr(ops(0, 3)); err != nil {
		t.Fatalf("successful batch returned %v", err)
	}

	err := insertBatchErr(ops(1, 2))
	if !errors.Is(err, gocb.ErrDocumentExists) {
		t.Fatalf("partial batch error %v does not wrap the document error", err)
	}
	for _, want := range []string{"1 of 2 documents failed", `"doc-0"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("partial batch error %q does not contain %q", err, want)
		}
	}

	err = insertBatchErr(ops(maxReportedBatchErrors+3, 10))
	if got := strings.Count(err.Error(), "document \""); got != maxReportedBatchErrors {
		t.Fatalf("large batch error listed %d documents, want %d: %v", got, maxReportedBatchErrors, err)
	}
	if !strings.Contains(err.Error(), "and 3 more") {
		t.Fatalf("large batch error does not count the unlisted failures: %v", err)
	}
}

// A dedicated client whose bucket warm-up fails is never returned, so the
// constructor must close it. This test is not parallel because it counts
// goroutines; Go runs it before any parallel test resumes.
func TestPerVUWarmupFailureClosesClient(t *testing.T) {
	cb := couchbaseFor(t, New())
	config := DBConfig{Hostname: "127.0.0.1", Username: "u", Password: "p"}
	failWarmup := func() {
		t.Helper()
		if client, err := cb.NewClientPerVU(config, []string{"missing"}, "10ms", 0); err == nil || client != nil {
			t.Fatalf("warm-up against no cluster succeeded: client=%v err=%v", client, err)
		}
	}

	// The first call starts any process-wide SDK goroutines.
	failWarmup()
	baseline := runtime.NumGoroutine()
	for range 5 {
		failWarmup()
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > baseline {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines grew from %d to %d after failed warm-ups", baseline, runtime.NumGoroutine())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A failed warm-up must not close the shared client other VUs already hold.
func TestSharedWarmupFailureKeepsClient(t *testing.T) {
	t.Parallel()
	root := New()
	cb := couchbaseFor(t, root)
	config := DBConfig{Hostname: "127.0.0.1", Username: "u", Password: "p"}
	shared, err := cb.NewClientWithSharedConnection(config, nil, "10ms", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cb.NewClientWithSharedConnection(config, []string{"missing"}, "10ms", 0); err == nil {
		t.Fatal("warm-up against no cluster succeeded")
	}
	if err := shared.Close(); err != nil {
		t.Fatalf("shared client was closed by a failed warm-up: %v", err)
	}
}
