import xk6_couchbase from 'k6/x/couchbase';
import { check } from 'k6';

const client = xk6_couchbase.newClient(__ENV.CB_HOST || 'localhost', __ENV.CB_USER || '<username>', __ENV.CB_PASS || '<password>');

const BUCKET = __ENV.CB_BUCKET || 'test';
const SCOPE = __ENV.CB_SCOPE || '_default';
const COLLECTION = __ENV.CB_COLLECTION || '_default';
const DOC_ID = 'exists-example';

// k6 exits 0 even when an iteration throws. Recording each iteration as a check
// and requiring every check to pass makes a failed operation fail the run.
export const options = { thresholds: { checks: ['rate==1'] } };

export default () => {
    let ok = false;
    try {
        // Seed the document first so this example is independently runnable.
        client.upsert(BUCKET, SCOPE, COLLECTION, DOC_ID, { title: 'Perf test experiment' });

        // syntax :: client.exists("<bucket>", "<scope>", "<collection>", "<docId>");
        // Returns true or false; throws only if the operation itself fails.
        const present = client.exists(BUCKET, SCOPE, COLLECTION, DOC_ID);
        const missing = client.exists(BUCKET, SCOPE, COLLECTION, `${DOC_ID}-missing-${__VU}-${__ITER}`);
        if (present !== true || missing !== false) {
            throw new Error(`exists returned ${present} for a seeded document and ${missing} for a missing one`);
        }
        ok = true;
    } finally {
        check(ok, { 'exists succeeded': value => value });
    }
}
