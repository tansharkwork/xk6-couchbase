# CouchBase k6 extension

K6 extension to perform tests on couchbase.

## Currently Supported Commands

- Supports inserting a document.
- Supports Batch insertion.
- Supports findOne (Fetch by primary key)
- Supports findMany (Batch fetch by primary keys)
- Supports deleting a document by id, with or without majority durability
- Supports upserts
- Testing query performance
- Prepared statement query performance

## Build Instructions

### For Development

```shell
git clone git@github.com:thotasrinath/xk6-couchbase.git
cd xk6-couchbase
make build
```

This produces a `./xk6-couchbase` binary from the local source. No global `xk6`
is required — the Makefile invokes it via `go run`. See the
[Development](#development) section for the full set of `make` targets (tests,
lint, example validation).

### For Use

Install xk6, then build a k6 binary with the published extension:

```shell
go install go.k6.io/xk6/cmd/xk6@latest
xk6 build --with github.com/thotasrinath/xk6-couchbase@latest
```

## Development

A `Makefile` wraps the common tasks (run `make` with no target to list them):

```shell
make build      # build a k6 binary with this extension from local source
make test       # run unit tests
make check      # gofmt + go vet + test (pre-commit gate)
make lint       # golangci-lint (see https://golangci-lint.run to install)
make validate   # build + ensure local Couchbase + run password examples end-to-end
make validate-cert  # build + provision Enterprise + run certificate acceptance
```

Tests exercise module lifecycle through JavaScript loading, shared/dedicated
connection isolation, TLS configuration errors, and real local mutual-TLS
handshakes. They generate certificates and require neither Couchbase nor Docker;
database authorization and operations are verified separately with the examples.

`make validate` runs password examples against a local Couchbase, starting a
Community container when needed. `make validate-cert` separately provisions
Enterprise and checks certificate success and rejection cases; neither default
CI nor `make validate` runs it. See [examples/README.md](examples/README.md).

## Examples

### Document Insertion Test

```js
import xk6_couchbase from 'k6/x/couchbase';


const client = xk6_couchbase.newClient('localhost', '<username>', '<password>');
export default () => {

    let doc = {
        correlationId: `test--couchbase`,
        title: 'Perf test experiment',
        url: 'example.com',
        locale: 'en',
        time: `${new Date(Date.now()).toISOString()}`
    };
    client.insert("test", "_default", "_default", makeId(15), doc);
}

function makeId(length) {
    let result = '';
    const characters = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
    const charactersLength = characters.length;
    let counter = 0;
    while (counter < length) {
        result += characters.charAt(Math.floor(Math.random() * charactersLength));
        counter += 1;
    }
    return result;
}

```

##### Below is the example commands to test the script

```bash
# Update examples/test-insert.js with cluster credentials and bucket name
 ./xk6-couchbase run --vus 10 --duration 30s examples/test-insert.js
```

### Batch Insert Documents

```js
import xk6_couchbase from 'k6/x/couchbase';


const client = xk6_couchbase.newClient('localhost', '<username>', '<password>');

const batchsize = 50;

export default () => {

    var docobjs = {}

    for (var i = 0; i < batchsize; i++) {
        docobjs[makeId(15)] = getRecord();
    }

    client.insertBatch("test", "_default", "_default", docobjs);
}

function getRecord() {
    return {
        correlationId: `test--couchbase`,
        title: 'Perf test experiment',
        url: 'example.com',
        locale: 'en',
        time: `${new Date(Date.now()).toISOString()}`
    };


}

function makeId(length) {
    let result = '';
    const characters = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
    const charactersLength = characters.length;
    let counter = 0;
    while (counter < length) {
        result += characters.charAt(Math.floor(Math.random() * charactersLength));
        counter += 1;
    }
    return result;
}

```

### Exists test

```js
import xk6_couchbase from 'k6/x/couchbase';


const client = xk6_couchbase.newClient('localhost', '<username>', '<password>');
export default () => {
    // syntax :: client.exists("<db>", "<scope>", "<keyspace>", "<docId>");
    // Returns true or false; throws only if the operation itself fails.
    var res = client.exists("test", "_default", "_default", "002wPJwiJArcUpz");
    console.log(res);
}

```


### FindOne test

```js
import xk6_couchbase from 'k6/x/couchbase';


const client = xk6_couchbase.newClient('localhost', '<username>', '<password>');
export default () => {
    // syntax :: client.findOne("<db>", "<scope>", "<keyspace>", "<docId>");
    var res = client.findOne("test", "_default", "_default", "002wPJwiJArcUpz");
    console.log(res);
}

```

### FindMany (Batch Get) test

```js
import xk6_couchbase from 'k6/x/couchbase';


const client = xk6_couchbase.newClient('localhost', '<username>', '<password>');
export default () => {
    // syntax :: client.findMany("<db>", "<scope>", "<keyspace>", ["<docId1>", "<docId2>", ...]);
    // Returns a map of docId -> document for keys that exist. Missing keys are omitted.
    var res = client.findMany("test", "_default", "_default", ["key1", "key2", "key3"]);
    console.log(JSON.stringify(res));
}

```

### Document Deletion Test

```js
import xk6_couchbase from 'k6/x/couchbase';


const client = xk6_couchbase.newClient('localhost', '<username>', '<password>');
export default () => {
    // Plain delete. The optional options object sets the timeout (default "1s").
    client.remove("test", "_default", "_default", "002wPJwiJArcUpz");
    client.remove("test", "_default", "_default", "002wPJwiJArcUpz", { timeout: "2s" });

    // Delete that waits for a majority of replicas to acknowledge it (default "1s").
    client.removeWithDurability("test", "_default", "_default", "002wPJwiJArcUpz", { timeout: "5s" });
}

```

`removeWithDurability` requires a bucket whose replicas the cluster can hold:
majority durability needs a majority of the document's copies (replicas + 1),
and each replica lives on a different node. On a single node, use a bucket with
0 replicas; otherwise the delete fails with "durability impossible".

### Query test

```js
import xk6_couchbase from 'k6/x/couchbase';


const client = xk6_couchbase.newClient('localhost', '<username>', '<password>');
export default () => {
    var res = client.find("select * from test._default._default  use keys \"00096zszpZaT47X\"");

    //console.log(res);

}
```

### Query using a prepared statement

```js
import xk6_couchbase from 'k6/x/couchbase';


const client = xk6_couchbase.newClient('localhost', '<username>', '<password>');
export default () => {
    var res = client.findByPreparedStmt("select * from test._default._default  use keys \"00096zszpZaT47X\"");

    //console.log(res);

}
```

### Upsert test

#### Initial data load is expected. Data with sequential Id's could be generated with below script. File - examples/load-sequential-data.js

```js
import xk6_couchbase from 'k6/x/couchbase';
const jobConfig = {
    vus: 10,
    count: 10000
}
export const options = {
    scenarios: {
        contacts: {
            executor: 'per-vu-iterations',
            vus: jobConfig.vus,
            iterations: jobConfig.count
        },
    },
};
const client = xk6_couchbase.newClient('localhost', '<username>', '<password>');
export default function () {
    let doc = {
        correlationId: `test--couchbase`,
        title: 'Perf test experiment',
        url: 'example.com',
        locale: 'en',
        time: `${new Date(Date.now()).toISOString()}`
    };

    let docId = ((__VU - 1) * jobConfig.count) + __ITER;
    client.insert("test", "_default", "_default", String(docId), doc);
}
```

#### Upsert test script to generate random id in range and excute Upsert. File - examples/test-upsert.js

```js
import xk6_couchbase from 'k6/x/couchbase';
import {randomIntBetween} from 'https://jslib.k6.io/k6-utils/1.2.0/index.js';

const client = xk6_couchbase.newClient('localhost', '<username>', '<password>');
export default () => {

    let doc = {
        correlationId: `test--couchbase`,
        title: 'Perf test experiment',
        url: 'example.com',
        locale: 'en',
        time: `${new Date(Date.now()).toISOString()}`,
        randomNo: randomIntBetween(0,1000000)
    };
    client.upsert("test", "_default", "_default", String(randomIntBetween(0, 100000)), doc);
}
```


### Client with options

Default NewClient is a good start to get familiar with the extension with little wiring and get going.  For refined flexibility of connection management during the tests,  following additional interfaces are provided:

* NewClientWithSharedConnection: Shares a single connection across all the VUs and maximizes the QPS without affecting client resources.
* NewClientPerVU: Creates new client connection for each VU. Useful for testing max number of connections that the cluster can handle.

In addition to controlling number of connections, both the functions provide:

* Ability to pre-warm the buckets as part of initialization.
* Ability to set timeout duration for bucket readiness (WaitUntilReady timeout).
* Ability to control the connection buffer size with the fourth constructor argument, in bytes.

#### Example for wiring client with shared connection across VUs

```js
import xk6_couchbase from 'k6/x/couchbase';


const dbConfig = { hostname: 'localhost', username: '<username>', password: '<password>' };
const bucketsToPreWarm = ['test'];
const client = xk6_couchbase.newClientWithSharedConnection(dbConfig, bucketsToPreWarm, "5s");
export default () => {
    // syntax :: client.findOne("<db>", "<scope>", "<keyspace>", "<docId>");
    var res = client.findOne("test", "_default", "_default", "002wPJwiJArcUpz");
    console.log(res);
}

```

#### Example for wiring client with unique connections per VUs

```javascript
import xk6_couchbase from 'k6/x/couchbase';


const dbConfig = { hostname: 'localhost', username: '<username>', password: '<password>' };
const bucketsToPreWarm = ['test'];
const client = xk6_couchbase.newClientPerVU(dbConfig, bucketsToPreWarm, "5s");
export default () => {
    // syntax :: client.findOne("<db>", "<scope>", "<keyspace>", "<docId>");
    var res = client.findOne("test", "_default", "_default", "002wPJwiJArcUpz");
    console.log(res);
}

```

## Client-certificate authentication

The config-based constructors support local PEM files with Couchbase Enterprise.
The [local acceptance recipe](examples/README.md#client-certificate-authentication)
uses Enterprise 8.0; certificate authentication is not restricted to that release.

```js
import couchbase from 'k6/x/couchbase';

const config = {
    hostname: 'couchbases://127.0.0.1',
    tls: {
        caCertPath: '/path/to/server-ca.pem',
        clientCertPath: '/path/to/client-cert.pem',
        clientKeyPath: '/path/to/client-key.pem',
    },
};
const client = couchbase.newClientWithSharedConnection(config, ['test'], '5s', 4096);
// newClientPerVU accepts the same arguments.
```

- Supply a client certificate/key pair **or** both username/password, never both
  authentication methods. Use the exact camelCase field names shown above.
- `caCertPath` optionally replaces SDK default server trust with a PEM CA bundle.
  Client keys must be unencrypted PEM. Server/hostname verification stays enabled.
- A `tls` object selects TLS for bare hosts; explicit `couchbase://` is rejected.
  CA-only TLS configuration supports password authentication over TLS.
- Paths resolve from the k6 process working directory; files are not included
  automatically in archives. Keep keys outside source control with private permissions.
- Shared certificates load once; restart the run to rotate them. Warming a
  bucket or performing an operation is required to verify server authentication.

### Compatibility changes

Shared mode previously ignored later settings. It now rejects a different host,
credentials, TLS file paths, or effective buffer size. Use identical settings
across VUs or dedicated clients. Cached creation failures take precedence over
configuration-conflict errors; restart the run after a creation failure.

`connectionBufferSizeBytes` is now applied: a positive constructor value overrides
URI `kv_buffer_size`; otherwise the URI value or 2048-byte default applies.
The size is allocated per connection, so large values multiply with
`newClientPerVU`. A non-positive or non-numeric URI `kv_buffer_size` is rejected.
Without certificates, missing username/password now fails at construction.
The positional `newClient(host, username, password)` remains a password API.

`remove` no longer requests majority durability, and its timeout changed from
a fixed 100ms to a configurable default of 1s. Use `removeWithDurability` for
the previous durability behavior. Existing `remove` calls need no changes.

`exists` now returns `true` or `false`. It previously returned `undefined` for
both present and missing documents. Scripts that ignore the result behave the
same; scripts that branch on it now get the correct answer. It still throws only
when the operation fails, never for a missing document.
