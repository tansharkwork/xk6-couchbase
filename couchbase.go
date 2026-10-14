// Package xk6_couchbase implements a k6 extension (k6/x/couchbase) for running
// load tests against Couchbase clusters.
package xk6_couchbase

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/couchbase/gocb/v2"
	k6modules "go.k6.io/k6/v2/js/modules"
)

func init() {
	k6modules.Register("k6/x/couchbase", New())
}

const (
	defaultBucketReadinessTimeout    = 5 * time.Second
	defaultDoConnectionPerVU         = true
	defaultConnectionBufferSizeBytes = 2048
	defaultRemoveTimeout             = time.Second
)

// RootModule is created once per k6 run. It holds state that is shared across
// all VUs, namely the lazily-initialized singleton client used in shared
// connection mode.
type RootModule struct {
	once             sync.Once
	singletonClient  *Client
	connectionConfig connectionConfig
	errz             error
}

// ModuleInstance is created once per VU by k6. Each instance points back to the
// single RootModule so VUs can share the singleton connection when requested.
type ModuleInstance struct {
	vu   k6modules.VU
	root *RootModule
}

// Ensure the module satisfies the k6 module interfaces.
var (
	_ k6modules.Module   = &RootModule{}
	_ k6modules.Instance = &ModuleInstance{}
)

// New builds the root module. It is registered once in init.
func New() *RootModule {
	return &RootModule{}
}

// NewModuleInstance is called by k6 for every VU, handing it the shared root.
func (r *RootModule) NewModuleInstance(vu k6modules.VU) k6modules.Instance {
	return &ModuleInstance{vu: vu, root: r}
}

// Exports exposes the CouchBase API object to the JS runtime as the default
// export, preserving the `import xk6_couchbase from 'k6/x/couchbase'` usage.
func (mi *ModuleInstance) Exports() k6modules.Exports {
	return k6modules.Exports{Default: &CouchBase{root: mi.root}}
}

// CouchBase is the JS-facing API object. Shared state lives on root.
type CouchBase struct {
	root *RootModule
}

type options struct {
	DoConnectionPerVU         bool          `json:"do_connection_per_vu,omitempty"`
	BucketReadinessTimeout    time.Duration `json:"bucket_readiness_timeout,omitempty"`
	BucketsToWarm             []string      `json:"buckets_to_warm,omitempty"`
	ConnectionBufferSizeBytes int           `json:"connection_buffer_size_bytes,omitempty"`
}

// DBConfig holds the connection details for a Couchbase cluster.
type DBConfig struct {
	Hostname string     `json:"connection_string,omitempty"`
	Username string     `json:"-"`
	Password string     `json:"-"`
	TLS      *TLSConfig `json:"tls,omitempty" js:"tls"`
}

// Client is a connected Couchbase client exposed to JS test scripts. It is
// returned by the NewClient* constructors and caches per-bucket connections.
type Client struct {
	cluster *gocb.Cluster
	options options

	// Key: bucketName (string)
	// Value: *gocb.Bucket
	bucketsConnections sync.Map
	mu                 sync.Mutex
}

// NewClientPerVU returns a client that opens a dedicated cluster connection for
// each VU. Useful for exercising the maximum number of connections a cluster
// can handle.
func (c *CouchBase) NewClientPerVU(dbConfig DBConfig, bucketsToWarm []string, bucketReadinessDuration string, connectionBufferSizeBytes int) (*Client, error) {
	opts := options{
		DoConnectionPerVU:         true,
		BucketReadinessTimeout:    parseStringToDuration(bucketReadinessDuration),
		BucketsToWarm:             bucketsToWarm,
		ConnectionBufferSizeBytes: connectionBufferSizeBytes,
	}
	return c.NewClientWithOptions(dbConfig, opts)
}

// NewClientWithSharedConnection returns a client backed by a single cluster
// connection shared across all VUs, maximizing QPS without exhausting client
// resources.
func (c *CouchBase) NewClientWithSharedConnection(dbConfig DBConfig, bucketsToWarm []string, bucketReadinessDuration string, connectionBufferSizeBytes int) (*Client, error) {
	opts := options{
		DoConnectionPerVU:         false,
		BucketReadinessTimeout:    parseStringToDuration(bucketReadinessDuration),
		BucketsToWarm:             bucketsToWarm,
		ConnectionBufferSizeBytes: connectionBufferSizeBytes,
	}

	return c.NewClientWithOptions(dbConfig, opts)
}

// NewClientWithOptions returns a client configured with the given options,
// optionally pre-warming the requested buckets.
func (c *CouchBase) NewClientWithOptions(dbConfig DBConfig, opts options) (*Client, error) {
	client, err := c.getCouchbaseInstance(dbConfig, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create new couchbase connection with options for cluster %s. Err: %w", dbConfig.Hostname, err)
	}

	// Optionally warm the bucket on client's request
	for _, bucket := range opts.BucketsToWarm {
		_, err := client.connectBucketOrLoad(bucket)
		if err != nil {
			// The caller never receives a dedicated client, so close it here.
			// The shared client stays open for the VUs already using it.
			if opts.DoConnectionPerVU {
				_ = client.Close()
			}
			return nil, fmt.Errorf("failed to connect to bucket :%s, Err: %w", bucket, err)
		}
	}
	return client, nil
}

// NewClient returns a dedicated client using password authentication. On
// success it returns the *Client; on failure it returns an error value to the
// JS runtime. Use a config-based constructor for client certificates.
func (c *CouchBase) NewClient(connectionString string, username string, password string) interface{} {
	dbConfig := DBConfig{
		Hostname: connectionString,
		Username: username,
		Password: password,
	}
	opts := options{
		DoConnectionPerVU:      defaultDoConnectionPerVU,
		BucketReadinessTimeout: defaultBucketReadinessTimeout,
	}
	client, err := c.getCouchbaseInstance(dbConfig, opts)
	if err != nil {
		return fmt.Errorf("failed to connect to couchase cluster %s. Err: %w", connectionString, err)
	}
	return client
}

// Insert adds a new document, failing if the document ID already exists.
func (c *Client) Insert(bucketName string, scope string, collection string, docID string, doc any) error {
	bucket, err := c.getBucket(bucketName)
	if err != nil {
		return fmt.Errorf("failed to create bucket connection for insert. Err: %w", err)
	}
	col := bucket.Scope(scope).Collection(collection)
	_, err = col.Insert(docID, doc, nil)
	if err != nil {
		return err
	}
	return nil
}

// Upsert inserts a document, replacing it if the document ID already exists.
func (c *Client) Upsert(bucketName string, scope string, collection string, docID string, doc any) error {
	bucket, err := c.getBucket(bucketName)
	if err != nil {
		return fmt.Errorf("failed to create bucket connection for upsert. Err: %w", err)
	}
	col := bucket.Scope(scope).Collection(collection)
	_, err = col.Upsert(docID, doc, nil)
	if err != nil {
		return err
	}
	return nil
}

// RemoveOptions is the optional trailing argument of remove and
// removeWithDurability. It is an object, not a bare duration, so scripts that
// pass an unrelated extra argument (as older README examples did) keep working.
type RemoveOptions struct {
	Timeout string `json:"timeout,omitempty" js:"timeout"`
}

func (o RemoveOptions) timeout() (time.Duration, error) {
	if o.Timeout == "" {
		return defaultRemoveTimeout, nil
	}
	timeout, err := time.ParseDuration(o.Timeout)
	if err != nil || timeout <= 0 {
		return 0, fmt.Errorf("remove timeout must be a positive duration such as \"1s\", got %q", o.Timeout)
	}
	return timeout, nil
}

// Remove deletes a document by ID without durability requirements. The
// optional opts.Timeout defaults to 1s.
func (c *Client) Remove(bucketName string, scope string, collection string, docID string, opts RemoveOptions) error {
	return c.remove(bucketName, scope, collection, docID, opts, gocb.DurabilityLevelNone)
}

// RemoveWithDurability deletes a document by ID and waits until a majority of
// replicas acknowledge it. The bucket's replica count must be satisfiable by
// the cluster's nodes. The optional opts.Timeout defaults to 1s.
func (c *Client) RemoveWithDurability(bucketName string, scope string, collection string, docID string, opts RemoveOptions) error {
	return c.remove(bucketName, scope, collection, docID, opts, gocb.DurabilityLevelMajority)
}

func (c *Client) remove(bucketName string, scope string, collection string, docID string, opts RemoveOptions, durability gocb.DurabilityLevel) error {
	timeout, err := opts.timeout()
	if err != nil {
		return err
	}
	bucket, err := c.getBucket(bucketName)
	if err != nil {
		return fmt.Errorf("failed to create bucket connection for remove. Err: %w", err)
	}
	col := bucket.Scope(scope).Collection(collection)
	_, err = col.Remove(docID, &gocb.RemoveOptions{
		Timeout:         timeout,
		DurabilityLevel: durability,
	})
	return err
}

// InsertBatch inserts multiple documents (keyed by document ID) in a single
// bulk operation. If any document fails, it returns an error naming the failed
// IDs; the other documents in the batch were still inserted.
func (c *Client) InsertBatch(bucketName string, scope string, collection string, docs map[string]any) error {
	bucket, err := c.getBucket(bucketName)
	if err != nil {
		return fmt.Errorf("failed to create bucket connection for insertBatch. Err: %w", err)
	}

	batchItems := make([]gocb.BulkOp, len(docs))
	index := 0
	for k, v := range docs {
		batchItems[index] = &gocb.InsertOp{ID: k, Value: v}
		index++
	}
	col := bucket.Scope(scope).Collection(collection)
	err = col.Do(batchItems, &gocb.BulkOpOptions{Timeout: 3 * time.Second})
	if err != nil {
		return err
	}

	return insertBatchErr(batchItems)
}

// maxReportedBatchErrors caps how many per-document failures an insertBatch
// error lists, so a large failed batch does not flood the k6 log.
const maxReportedBatchErrors = 5

// insertBatchErr reports the per-document failures that Collection.Do leaves
// on each operation rather than returning.
func insertBatchErr(ops []gocb.BulkOp) error {
	var errs []error
	failed := 0
	for _, op := range ops {
		insert := op.(*gocb.InsertOp)
		if insert.Err == nil {
			continue
		}
		failed++
		if len(errs) < maxReportedBatchErrors {
			errs = append(errs, fmt.Errorf("document %q: %w", insert.ID, insert.Err))
		}
	}
	if failed == 0 {
		return nil
	}
	if failed > len(errs) {
		errs = append(errs, fmt.Errorf("and %d more", failed-len(errs)))
	}
	return fmt.Errorf("insertBatch: %d of %d documents failed; the others were inserted: %w", failed, len(ops), errors.Join(errs...))
}

// FindMany fetches multiple documents by key in a single bulk operation,
// returning a map of document ID to document. Keys that do not exist (or fail
// to decode) are omitted from the result; any other per-key failure, such as a
// timeout, is returned as an error.
func (c *Client) FindMany(bucketName string, scope string, collection string, keys []string) (map[string]interface{}, error) {
	bucket, err := c.getBucket(bucketName)
	if err != nil {
		return nil, fmt.Errorf("failed to get bucket connection for findMany. Err: %w", err)
	}
	col := bucket.Scope(scope).Collection(collection)

	batchItems := make([]gocb.BulkOp, len(keys))
	for i, key := range keys {
		batchItems[i] = &gocb.GetOp{ID: key}
	}

	err = col.Do(batchItems, &gocb.BulkOpOptions{Timeout: 3 * time.Second})
	if err != nil {
		return nil, err
	}

	results := make(map[string]interface{}, len(keys))
	for _, op := range batchItems {
		getOp := op.(*gocb.GetOp)
		if errors.Is(getOp.Err, gocb.ErrDocumentNotFound) {
			continue
		}
		if getOp.Err != nil {
			return nil, fmt.Errorf("findMany: document %q: %w", getOp.ID, getOp.Err)
		}
		var doc interface{}
		if err := getOp.Result.Content(&doc); err != nil {
			// Skip keys whose content fails to decode.
			continue
		}
		results[getOp.ID] = doc
	}

	return results, nil
}

// Find executes a N1QL query without preparing it and returns the last row
// read. Use FindByPreparedStmt for a statement that runs repeatedly.
func (c *Client) Find(query string) (any, error) {
	queryResult, err := c.cluster.Query(
		query,
		&gocb.QueryOptions{Adhoc: true},
	)
	if err != nil {
		return nil, err
	}
	return lastRow(queryResult)
}

// Exists reports whether a document with the given ID exists. A missing
// document returns false; only operation failures return an error.
func (c *Client) Exists(bucketName string, scope string, collection string, docID string) (bool, error) {
	bucket, err := c.getBucket(bucketName)
	if err != nil {
		return false, fmt.Errorf("failed to get bucket connection for exists. Err: %w", err)
	}
	bucketScope := bucket.Scope(scope)
	result, err := bucketScope.Collection(collection).Exists(docID, nil)
	if err != nil {
		return false, err
	}
	return result.Exists(), nil
}

// FindOne fetches a single document by ID.
func (c *Client) FindOne(bucketName string, scope string, collection string, docID string) (any, error) {
	var result interface{}
	bucket, err := c.getBucket(bucketName)
	if err != nil {
		return result, fmt.Errorf("failed to get bucket connection for findOne. Err: %w", err)
	}
	bucketScope := bucket.Scope(scope)
	getResult, err := bucketScope.Collection(collection).Get(docID, nil)
	if err != nil {
		return result, err
	}

	err = getResult.Content(&result)
	if err != nil {
		return result, err
	}

	return result, nil
}

// FindByPreparedStmt executes a N1QL query as a prepared statement with
// positional parameters and returns the last row read. The SDK prepares each
// distinct statement once and reuses the plan on later calls.
func (c *Client) FindByPreparedStmt(query string, params ...interface{}) (any, error) {
	queryResult, err := c.cluster.Query(
		query,
		&gocb.QueryOptions{
			PositionalParameters: params,
		},
	)
	if err != nil {
		return nil, err
	}
	return lastRow(queryResult)
}

// queryRows is the part of *gocb.QueryResult that lastRow uses, so its error
// handling can be tested without a cluster.
type queryRows interface {
	Next() bool
	Row(valuePtr interface{}) error
	Err() error
	Close() error
}

// lastRow reads every row and returns the last one. A query can fail after
// streaming rows; that failure is reported only by Err, after Next returns
// false, so it must be checked before returning a row as a success.
func lastRow(rows queryRows) (result any, err error) {
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			result, err = nil, closeErr
		}
	}()
	for rows.Next() {
		if err := rows.Row(&result); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// Close closes the underlying cluster connection.
func (c *Client) Close() error {
	opts := gocb.ClusterCloseOptions{}
	return c.cluster.Close(&opts)
}

// TODO: Create bucket connections on inits and remove mutex.
func (c *Client) getBucket(bucketName string) (*gocb.Bucket, error) {
	return c.connectBucketOrLoad(bucketName)
}

func (c *Client) connectBucketOrLoad(bucketName string) (*gocb.Bucket, error) {
	bucket, found := c.bucketsConnections.Load(bucketName)
	if !found || bucket == nil {
		// Create bucket connections
		// Mutex Lock to ensure that the bucket is instantiated only once in shared cluster connection mode.
		c.mu.Lock()
		defer c.mu.Unlock()
		bucket, found := c.bucketsConnections.Load(bucketName)
		if found && bucket != nil {
			return bucket.(*gocb.Bucket), nil
		}

		newBucket := c.cluster.Bucket(bucketName)
		err := newBucket.WaitUntilReady(c.options.BucketReadinessTimeout, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to wait for bucket %s, timeout: %v. Err: %w", bucketName, c.options.BucketReadinessTimeout, err)
		}
		c.bucketsConnections.Store(bucketName, newBucket)
		return newBucket, nil

	}
	return bucket.(*gocb.Bucket), nil
}

func (c *CouchBase) getCouchbaseInstance(dbConfig DBConfig, opts options) (*Client, error) {
	config, err := normalizeConnectionConfig(dbConfig, opts.ConnectionBufferSizeBytes)
	if err != nil {
		return nil, err
	}
	if opts.DoConnectionPerVU {
		return instantiateNewConnection(config, opts)
	}

	c.root.once.Do(func() {
		c.root.connectionConfig = config
		c.root.singletonClient, c.root.errz = instantiateNewConnection(config, opts)
	})
	if c.root.errz != nil {
		return nil, c.root.errz
	}
	if config != c.root.connectionConfig {
		return nil, fmt.Errorf("shared connection configuration differs from the first initialization; use the same host, credentials, TLS files, and buffer size or use newClientPerVU")
	}
	return c.root.singletonClient, c.root.errz
}

func instantiateNewConnection(config connectionConfig, options options) (*Client, error) {
	clusterOptions, err := config.clusterOptions()
	if err != nil {
		return nil, err
	}
	cluster, err := gocb.Connect(config.connectionString, clusterOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate couchbase connection: %w", err)
	}

	return &Client{cluster: cluster, options: options}, nil
}

func parseStringToDuration(bucketReadinessDuration string) time.Duration {
	readinessDuration, err := time.ParseDuration(bucketReadinessDuration)
	if err != nil {
		readinessDuration = defaultBucketReadinessTimeout
	}

	return readinessDuration
}
