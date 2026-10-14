import xk6_couchbase from 'k6/x/couchbase';
import { check } from 'k6';

const client = xk6_couchbase.newClient(__ENV.CB_HOST || 'localhost', __ENV.CB_USER || '<username>', __ENV.CB_PASS || '<password>');

const BUCKET = __ENV.CB_BUCKET || 'test';
const SCOPE = __ENV.CB_SCOPE || '_default';
const COLLECTION = __ENV.CB_COLLECTION || '_default';

const batchsize = 50;

// k6 exits 0 even when an iteration throws. Recording each iteration as a check
// and requiring every check to pass makes a failed operation fail the run.
export const options = { thresholds: { checks: ['rate==1'] } };

export default () => {
    let ok = false;
    try {
        var docobjs = {}

        for (var i = 0; i < batchsize; i++) {
            docobjs[makeId(15)] = getRecord();
        }

        client.insertBatch(BUCKET, SCOPE, COLLECTION, docobjs);

        // A batch that reuses an existing ID throws, but its other documents are
        // still inserted.
        const existingId = Object.keys(docobjs)[0];
        const newId = makeId(15);
        let failed = false;
        try {
            client.insertBatch(BUCKET, SCOPE, COLLECTION, { [existingId]: getRecord(), [newId]: getRecord() });
        } catch (error) {
            failed = true;
        }
        if (!failed || client.exists(BUCKET, SCOPE, COLLECTION, newId) !== true) {
            throw new Error(`insertBatch with a duplicate ID: threw=${failed}, new document inserted=${client.exists(BUCKET, SCOPE, COLLECTION, newId)}`);
        }
        ok = true;
    } finally {
        check(ok, { 'insertBatch succeeded': value => value });
    }
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
