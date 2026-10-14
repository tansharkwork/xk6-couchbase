import xk6_couchbase from 'k6/x/couchbase';
import { check } from 'k6';

const BUCKET = __ENV.CB_BUCKET || 'test';
const SCOPE = __ENV.CB_SCOPE || '_default';
const COLLECTION = __ENV.CB_COLLECTION || '_default';
const DOC_ID = 'shared-conn-example';

// newClientWithSharedConnection shares a single cluster connection across all
// VUs, maximizing QPS without exhausting client resources.
const dbConfig = { hostname: __ENV.CB_HOST || 'localhost', username: __ENV.CB_USER || '<username>', password: __ENV.CB_PASS || '<password>' };
const bucketsToPreWarm = [BUCKET];
const client = xk6_couchbase.newClientWithSharedConnection(dbConfig, bucketsToPreWarm, "5s");

// k6 exits 0 even when an iteration throws. Recording each iteration as a check
// and requiring every check to pass makes a failed operation fail the run.
export const options = { thresholds: { checks: ['rate==1'] } };

export default () => {
    let ok = false;
    try {
        // Seed the document first so this example is independently runnable.
        client.upsert(BUCKET, SCOPE, COLLECTION, DOC_ID, {
            correlationId: 'test--couchbase',
            title: 'Perf test experiment',
        });

        // syntax :: client.findOne("<bucket>", "<scope>", "<collection>", "<docId>");
        const res = client.findOne(BUCKET, SCOPE, COLLECTION, DOC_ID);
        console.log(JSON.stringify(res));
        ok = true;
    } finally {
        check(ok, { 'shared client succeeded': value => value });
    }
}
