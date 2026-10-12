import xk6_couchbase from 'k6/x/couchbase';

const client = xk6_couchbase.newClient(__ENV.CB_HOST || 'localhost', __ENV.CB_USER || '<username>', __ENV.CB_PASS || '<password>');

const BUCKET = __ENV.CB_BUCKET || 'test';
const SCOPE = __ENV.CB_SCOPE || '_default';
const COLLECTION = __ENV.CB_COLLECTION || '_default';
const DOC_ID = 'exists-example';

export default () => {
    // Seed the document first so this example is independently runnable.
    client.upsert(BUCKET, SCOPE, COLLECTION, DOC_ID, { title: 'Perf test experiment' });

    // syntax :: client.exists("<bucket>", "<scope>", "<collection>", "<docId>");
    // Returns true or false; throws only if the operation itself fails.
    const present = client.exists(BUCKET, SCOPE, COLLECTION, DOC_ID);
    const missing = client.exists(BUCKET, SCOPE, COLLECTION, `${DOC_ID}-missing-${__VU}-${__ITER}`);
    if (present !== true || missing !== false) {
        throw new Error(`exists returned ${present} for a seeded document and ${missing} for a missing one`);
    }
}
