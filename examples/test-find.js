import xk6_couchbase from 'k6/x/couchbase';

const client = xk6_couchbase.newClient(__ENV.CB_HOST || 'localhost', __ENV.CB_USER || '<username>', __ENV.CB_PASS || '<password>');

const BUCKET = __ENV.CB_BUCKET || 'test';
const SCOPE = __ENV.CB_SCOPE || '_default';
const COLLECTION = __ENV.CB_COLLECTION || '_default';
const DOC_ID = 'find-example';

export default () => {
    // Seed the document first so this example is independently runnable.
    client.upsert(BUCKET, SCOPE, COLLECTION, DOC_ID, {
        correlationId: 'test--couchbase',
        title: 'Perf test experiment',
    });

    // USE KEYS performs a key lookup through the query service and needs no
    // secondary/primary index, so this runs on a fresh bucket.
    const keyspace = `\`${BUCKET}\`.\`${SCOPE}\`.\`${COLLECTION}\``;
    const res = client.find(`select * from ${keyspace} use keys "${DOC_ID}"`);
    console.log(JSON.stringify(res));

    // The same lookup as a prepared statement with a positional parameter.
    const prepared = client.findByPreparedStmt(`select * from ${keyspace} use keys $1`, DOC_ID);
    for (const row of [res, prepared]) {
        if (!row || row[COLLECTION].title !== 'Perf test experiment') {
            throw new Error(`query did not return the seeded document: ${JSON.stringify(row)}`);
        }
    }

    // A query can fail after streaming rows. find throws rather than returning
    // the rows read before the failure.
    let failed = false;
    try {
        client.find('select case when x < 3 then x else abort("stop") end as v from array_range(0, 5) as x');
    } catch (error) {
        failed = true;
    }
    if (!failed) {
        throw new Error('find reported success for a query that failed after streaming rows');
    }
}
