# Couchbase k6 extension

A [k6](https://k6.io) extension (`k6/x/couchbase`) for load testing Couchbase.

This repository, [tansharkwork/xk6-couchbase](https://github.com/tansharkwork/xk6-couchbase),
is a fork of [thotasrinath/xk6-couchbase](https://github.com/thotasrinath/xk6-couchbase).
Scripts written for the upstream extension run here unchanged, apart from the
behavior differences listed in
[Differences from the upstream extension](#differences-from-the-upstream-extension).

## About this fork

The fork adds:

- Client-certificate (mTLS) authentication for Couchbase Enterprise, with
  verified TLS and an optional custom CA ([details](#client-certificate-authentication)).
- `removeWithDurability`, with `remove` and `removeWithDurability` both taking
  a configurable timeout.
- An `exists` that returns `true` or `false`.
- Failures that upstream reported as success now throw: query errors after
  rows have streamed, per-document `insertBatch` failures, and `findMany`
  failures other than a missing document.
- `findByPreparedStmt` runs a prepared statement; `find` runs an unprepared one.
- Full Couchbase connection strings (`couchbase://`, `couchbases://`, multiple
  hosts, query options) and a working `connectionBufferSizeBytes` argument.
- Make targets and scripts that run every example against local Couchbase
  Community and Enterprise containers.

The Go module path is still `github.com/thotasrinath/xk6-couchbase`, which keeps
the fork easy to merge upstream. Build it with the `--with` replacement shown
under [For Use](#for-use); the JavaScript import stays `k6/x/couchbase`.

## Supported operations

| Operation | JavaScript |
| --- | --- |
| Insert one document | `insert(bucket, scope, collection, id, doc)` |
| Insert many documents | `insertBatch(bucket, scope, collection, { id: doc, ... })` |
| Upsert one document | `upsert(bucket, scope, collection, id, doc)` |
| Get one document | `findOne(bucket, scope, collection, id)` |
| Get many documents | `findMany(bucket, scope, collection, [ids])` |
| Check whether a document exists | `exists(bucket, scope, collection, id)`, returns `true`/`false` |
| Delete a document | `remove(bucket, scope, collection, id, { timeout })` |
| Delete with majority durability | `removeWithDurability(bucket, scope, collection, id, { timeout })` |
| N1QL query | `find(statement)` |
| Parameterized N1QL query | `findByPreparedStmt(statement, ...params)` |

Clients are created with `newClient`, `newClientWithSharedConnection`, or
`newClientPerVU`; see [Client with options](#client-with-options).

## Build Instructions

### For Development

```shell
git clone git@github.com:tansharkwork/xk6-couchbase.git
cd xk6-couchbase
make build
```

This produces a `./xk6-couchbase` binary from the local source. No global `xk6`
is required — the Makefile invokes it via `go run`. See the
[Development](#development) section for the full set of `make` targets (tests,
lint, example validation).

### For Use

#### Prebuilt binaries

Releases after v0.1.0 include k6 binaries with this extension built in, so no
Go or xk6 installation is needed. Download the archive for your platform from
[Releases](https://github.com/tansharkwork/xk6-couchbase/releases) (Linux and
macOS on amd64 and arm64, Windows on amd64), verify it against
`checksums.txt`, and run the binary in place of `k6`:

```shell
VERSION=v0.2.2 PLATFORM=linux_amd64   # e.g. darwin_arm64 for Apple silicon
curl -LO "https://github.com/tansharkwork/xk6-couchbase/releases/download/$VERSION/xk6-couchbase_${VERSION}_${PLATFORM}.tar.gz"
curl -LO "https://github.com/tansharkwork/xk6-couchbase/releases/download/$VERSION/checksums.txt"
sha256sum --ignore-missing -c checksums.txt   # macOS: shasum -a 256 --ignore-missing -c checksums.txt
tar -xzf "xk6-couchbase_${VERSION}_${PLATFORM}.tar.gz"
"./xk6-couchbase_${VERSION}_${PLATFORM}/xk6-couchbase" run script.js
```

The release notes state which k6 version each binary bundles. The macOS
binaries are not signed; if macOS blocks a browser-downloaded binary, clear the
quarantine flag with `xattr -d com.apple.quarantine xk6-couchbase`. To use a
different k6 version or combine this with other extensions, build with xk6.

#### Build with xk6

Building requires Go 1.27.3 or newer. With Go's default `GOTOOLCHAIN=auto`, an
older local Go downloads the required toolchain automatically.

Install xk6, then build a k6 binary with this fork's extension:

```shell
go install go.k6.io/xk6/cmd/xk6@latest
xk6 build --output xk6-couchbase \
  --with github.com/thotasrinath/xk6-couchbase=github.com/tansharkwork/xk6-couchbase@v0.2.2
```

The `module=replacement` form is required because the module path still names
the upstream repository. Without the replacement,
`--with github.com/thotasrinath/xk6-couchbase@latest` builds the upstream
extension, which lacks this fork's features. Use a newer
[release](https://github.com/tansharkwork/xk6-couchbase/releases) when one is
available, or `@master` for the latest unreleased changes.

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
Enterprise and checks certificate success and rejection cases; `make validate`
does not run it. See [examples/README.md](examples/README.md).

### Continuous integration

GitHub Actions workflows live in [.github/workflows](.github/workflows):

| Workflow | Runs on | What it checks |
|---|---|---|
| CI | Pushes and pull requests to `master` | golangci-lint, `go mod tidy`, race-enabled unit tests, and `make validate` against Couchbase Community |
| Certificate validation | Manual dispatch, or changes to the extension, the examples and scripts the suite runs, the `Makefile`, or `go.mod`/`go.sum` | `make validate-cert` against Couchbase Enterprise |
| govulncheck | Pushes, pull requests, and weekly | Known vulnerabilities reachable from this module |
| Secret scan | Pushes and pull requests to `master` | gitleaks scan of the full git history for committed credentials |
| Release | Pushing a `v*` tag | Builds k6 binaries for each platform and publishes them, with checksums and generated notes, as a GitHub release |

Dependabot opens weekly grouped updates for Go modules and GitHub Actions, and
security update pull requests when an advisory affects a dependency.

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

To run the bundled version of this script against your cluster:

```bash
./xk6-couchbase run --vus 10 --duration 30s \
  -e CB_HOST=localhost -e CB_USER=Administrator -e CB_PASS=password examples/test-insert.js
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

    // Throws if any document fails (for example, an ID that already exists).
    // The other documents in the batch are still inserted.
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
    // Returns a map of docId -> document for keys that exist. Missing keys are
    // omitted; any other per-key failure, such as a timeout, throws.
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
    // Runs the statement unprepared and returns the last row of the result.
    // Throws if the query fails, including after some rows have streamed.
    var res = client.find("select * from test._default._default use keys \"00096zszpZaT47X\"");
    console.log(res);
}
```

### Query using a prepared statement

```js
import xk6_couchbase from 'k6/x/couchbase';


const client = xk6_couchbase.newClient('localhost', '<username>', '<password>');
export default () => {
    // Positional parameters follow the statement. Returns the last row of the result.
    // The SDK prepares each distinct statement once and reuses the plan.
    var res = client.findByPreparedStmt("select * from test._default._default use keys $1", "00096zszpZaT47X");
    console.log(res);
}
```

### Upsert test

#### Initial data load is expected. Data with sequential IDs can be generated with this script (`examples/load-sequential-data.js`)

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

#### Upsert a random ID within the loaded range (`examples/test-upsert.js`)

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

`newClient(host, username, password)` is the quickest way to get started. It
creates a dedicated connection with password authentication. For more control
over connections, use:

* `newClientWithSharedConnection(config, bucketsToWarm, readinessTimeout, bufferSizeBytes)`:
  shares one connection across all VUs, maximizing QPS without exhausting client resources.
* `newClientPerVU(config, bucketsToWarm, readinessTimeout, bufferSizeBytes)`:
  creates a connection for each VU. Useful for testing how many connections the cluster can handle.

`config` is `{ hostname, username, password }`, or `{ hostname, tls: {...} }` for
[client certificates](#client-certificate-authentication). Both constructors can:

* Pre-warm buckets during initialization (`bucketsToWarm`).
* Set the bucket readiness timeout, such as `"5s"`.
* Set the connection buffer size in bytes (optional fourth argument, default 2048).

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
import xk6_couchbase from 'k6/x/couchbase';

const config = {
    hostname: 'couchbases://127.0.0.1',
    tls: {
        caCertPath: '/path/to/server-ca.pem',
        clientCertPath: '/path/to/client-cert.pem',
        clientKeyPath: '/path/to/client-key.pem',
    },
};
const client = xk6_couchbase.newClientWithSharedConnection(config, ['test'], '5s', 4096);
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

## Differences from the upstream extension

If you are moving scripts from
[thotasrinath/xk6-couchbase](https://github.com/thotasrinath/xk6-couchbase),
these behaviors differ:

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

`find` and `findByPreparedStmt` now throw when a query fails after streaming
rows. Previously they returned the last row read as a success.

`insertBatch` now throws when any document fails, for example because its ID
already exists. The error names up to five failed IDs. Documents that did not
fail are still inserted, so a thrown batch may be partly written. Previously
these failures were silent.

`findMany` still omits missing documents but now throws on other per-key
failures, such as timeouts. Previously those keys were silently omitted.

`findByPreparedStmt` now runs a prepared statement, and `find` runs an
unprepared one. Upstream had these reversed, so `find` prepared every distinct
statement while `findByPreparedStmt` prepared none. Compare query performance
measured before this change with that in mind.
