import xk6_couchbase from 'k6/x/couchbase';
import { check } from 'k6';

const jobConfig = {
    vus: 10,
    count: 10000
}

// k6 exits 0 even when an iteration throws. Recording each iteration as a check
// and requiring every check to pass makes a failed operation fail the run.
export const options = {
    thresholds: { checks: ['rate==1'] },
    scenarios: {
        contacts: {
            executor: 'per-vu-iterations',
            vus: jobConfig.vus,
            iterations: jobConfig.count
        },
    },
};

const BUCKET = __ENV.CB_BUCKET || 'test';
const SCOPE = __ENV.CB_SCOPE || '_default';
const COLLECTION = __ENV.CB_COLLECTION || '_default';

const client = xk6_couchbase.newClient(__ENV.CB_HOST || 'localhost', __ENV.CB_USER || '<username>', __ENV.CB_PASS || '<password>');

export default function () {
    let ok = false;
    try {
        let doc = {
            correlationId: `test--couchbase`,
            title: 'Perf test experiment',
            url: 'example.com',
            locale: 'en',
            time: `${new Date(Date.now()).toISOString()}`
        };

        let docId = ((__VU - 1) * jobConfig.count) + __ITER;
        client.insert(BUCKET, SCOPE, COLLECTION, String(docId), doc);
        ok = true;
    } finally {
        check(ok, { 'sequential load succeeded': value => value });
    }
}
