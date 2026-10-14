import xk6_couchbase from 'k6/x/couchbase';
import { check } from 'k6';
import {randomIntBetween} from 'https://jslib.k6.io/k6-utils/1.2.0/index.js';

const BUCKET = __ENV.CB_BUCKET || 'test';
const SCOPE = __ENV.CB_SCOPE || '_default';
const COLLECTION = __ENV.CB_COLLECTION || '_default';

const client = xk6_couchbase.newClient(__ENV.CB_HOST || 'localhost', __ENV.CB_USER || '<username>', __ENV.CB_PASS || '<password>');

// k6 exits 0 even when an iteration throws. Recording each iteration as a check
// and requiring every check to pass makes a failed operation fail the run.
export const options = { thresholds: { checks: ['rate==1'] } };

export default () => {
    let ok = false;
    try {
        let doc = {
            correlationId: `test--couchbase`,
            title: 'Perf test experiment',
            url: 'example.com',
            locale: 'en',
            time: `${new Date(Date.now()).toISOString()}`,
            randomNo: randomIntBetween(0,1000000)
        };
        client.upsert(BUCKET, SCOPE, COLLECTION, String(randomIntBetween(0, 100000)), doc);
        ok = true;
    } finally {
        check(ok, { 'upsert succeeded': value => value });
    }
}

