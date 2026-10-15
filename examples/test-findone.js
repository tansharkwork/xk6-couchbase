import xk6_couchbase from 'k6/x/couchbase';
import { check } from 'k6';

const client = xk6_couchbase.newClient(__ENV.CB_HOST || 'localhost', __ENV.CB_USER || '<username>', __ENV.CB_PASS || '<password>');

const BUCKET = __ENV.CB_BUCKET || 'test';
const SCOPE = __ENV.CB_SCOPE || '_default';
const COLLECTION = __ENV.CB_COLLECTION || '_default';
const DOC_ID = 'findone-example';

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
        check(ok, { 'findOne succeeded': value => value });
    }
}
