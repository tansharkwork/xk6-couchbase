import xk6_couchbase from 'k6/x/couchbase';
import { check } from 'k6';

const client = xk6_couchbase.newClient(__ENV.CB_HOST || 'localhost', __ENV.CB_USER || '<username>', __ENV.CB_PASS || '<password>');

const BUCKET = __ENV.CB_BUCKET || 'test';
const SCOPE = __ENV.CB_SCOPE || '_default';
const COLLECTION = __ENV.CB_COLLECTION || '_default';

function assertRemoved(docId) {
    if (client.exists(BUCKET, SCOPE, COLLECTION, docId) !== false) {
        throw new Error(`${docId} still exists after remove`);
    }
}

// k6 exits 0 even when an iteration throws. Recording each iteration as a check
// and requiring every check to pass makes a failed operation fail the run.
export const options = { thresholds: { checks: ['rate==1'] } };

export default () => {
    let ok = false;
    try {
        // Seed each document first so this example is independently runnable.
        const plainId = `remove-example-${__VU}-${__ITER}`;
        client.upsert(BUCKET, SCOPE, COLLECTION, plainId, { title: 'remove me' });
        // syntax :: client.remove("<bucket>", "<scope>", "<collection>", "<docId>", { timeout: "1s" });
        client.remove(BUCKET, SCOPE, COLLECTION, plainId, { timeout: '2s' });
        assertRemoved(plainId);

        // Waits for a majority of replicas; the bucket's replicas must fit on the cluster's nodes.
        const durableId = `remove-durable-example-${__VU}-${__ITER}`;
        client.upsert(BUCKET, SCOPE, COLLECTION, durableId, { title: 'remove me durably' });
        client.removeWithDurability(BUCKET, SCOPE, COLLECTION, durableId, { timeout: '5s' });
        assertRemoved(durableId);
        ok = true;
    } finally {
        check(ok, { 'remove succeeded': value => value });
    }
}
