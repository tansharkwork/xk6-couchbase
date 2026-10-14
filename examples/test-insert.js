import xk6_couchbase from 'k6/x/couchbase';
import { check } from 'k6';

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
            time: `${new Date(Date.now()).toISOString()}`
        };
        client.insert(BUCKET, SCOPE, COLLECTION, makeId(15), doc);
        ok = true;
    } finally {
        check(ok, { 'insert succeeded': value => value });
    }
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
