import couchbase from 'k6/x/couchbase';
import { check } from 'k6';

export const options = {
    vus: 1,
    iterations: 1,
    thresholds: { checks: ['rate==1'] },
};

if (!__ENV.CB_HOST || !__ENV.CB_CLIENT_CERT || !__ENV.CB_CLIENT_KEY) {
    throw new Error('Set CB_HOST, CB_CLIENT_CERT, and CB_CLIENT_KEY for certificate authentication');
}
const mode = __ENV.CB_CONNECTION_MODE || 'shared';
if (mode !== 'shared' && mode !== 'per-vu') {
    throw new Error('CB_CONNECTION_MODE must be shared or per-vu');
}

const bucket = __ENV.CB_BUCKET || 'test';
const scope = __ENV.CB_SCOPE || '_default';
const collection = __ENV.CB_COLLECTION || '_default';
const config = {
    hostname: __ENV.CB_HOST,
    tls: {
        caCertPath: __ENV.CB_CA_CERT || '',
        clientCertPath: __ENV.CB_CLIENT_CERT,
        clientKeyPath: __ENV.CB_CLIENT_KEY,
    },
};
const client = mode === 'shared'
    ? couchbase.newClientWithSharedConnection(config, [bucket], '5s')
    : couchbase.newClientPerVU(config, [bucket], '5s');

export default function () {
    const id = `client-cert-example-${__VU}-${__ITER}`;
    const doc = { marker: id, authentication: 'client-certificate' };
    try {
        client.upsert(bucket, scope, collection, id, doc);
        const got = client.findOne(bucket, scope, collection, id);
        check(got, { 'certificate-authenticated KV roundtrip': value => value.marker === id });

        const keyspace = [bucket, scope, collection]
            .map(name => `\`${name.replace(/`/g, '``')}\``).join('.');
        const row = client.find(`SELECT RAW d FROM ${keyspace} AS d USE KEYS ${JSON.stringify(id)}`);
        check(row, { 'certificate-authenticated query': value => value.marker === id });
    } catch (error) {
        check(false, { 'certificate-authenticated operations succeeded': value => value });
        throw error;
    }
}
