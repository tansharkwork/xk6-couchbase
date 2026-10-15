import xk6_couchbase from 'k6/x/couchbase';
import { check } from 'k6';

const BUCKET = __ENV.CB_BUCKET || 'test';
const SCOPE = __ENV.CB_SCOPE || '_default';
const COLLECTION = __ENV.CB_COLLECTION || '_default';
const DOC_ID = 'shared-conn-example';

// newClientWithSharedConnection shares a single cluster connection across all
// VUs, maximizing QPS without exhausting client resources.
const dbConfig = { hostname: __ENV.CB_HOST || 'localhost', username: __ENV.CB_USER || '<username>', password: __ENV.CB_PASS || '<password>' };
// Optional password authentication over verified TLS: set CB_HOST to a
// couchbases:// connection string and CB_CA_CERT to the server's CA bundle.
if (__ENV.CB_CA_CERT) {
    dbConfig.tls = { caCertPath: __ENV.CB_CA_CERT };
}
const bucketsToPreWarm = [BUCKET];
const client = xk6_couchbase.newClientWithSharedConnection(dbConfig, bucketsToPreWarm, "5s");

// k6 exits 0 even when an iteration throws. Recording each iteration as a check
// and requiring every check to pass makes a failed operation fail the run.
export const options = { thresholds: { checks: ['rate==1'] } };

export default () => {
    let ok = false;
    try {
        // Seed the document first so this example is independently runnable.
        // Each VU writes its own document with a fresh marker, so reading back
        // the marker proves this iteration's write was returned.
        const docId = `${DOC_ID}-${__VU}`;
        const marker = `${__VU}-${__ITER}-${Date.now()}`;
        client.upsert(BUCKET, SCOPE, COLLECTION, docId, {
            correlationId: 'test--couchbase',
            title: 'Perf test experiment',
            marker,
        });

        // syntax :: client.findOne("<bucket>", "<scope>", "<collection>", "<docId>");
        const res = client.findOne(BUCKET, SCOPE, COLLECTION, docId);
        console.log(JSON.stringify(res));
        if (!res || res.marker !== marker || res.title !== 'Perf test experiment') {
            throw new Error(`findOne did not return the document just written: ${JSON.stringify(res)}`);
        }
        ok = true;
    } finally {
        check(ok, { 'shared client succeeded': value => value });
    }
}
