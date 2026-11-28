// Runs inside the short-lived provisioning container. Credentials come from
// mounted files. The gateway only receives the control database URI.
const fs = require('fs');
const secret = name => fs.readFileSync('/run/secrets/' + name, 'utf8').trim();
const server = new Mongo('mongodb://mongo:27017');
const admin = server.getDB('admin');
if (!admin.auth('bootstrap', secret('root_password')).ok) {
  throw Error('MongoDB bootstrap authentication failed');
}
const control = server.getDB('mlc_gateway');
if (!control.getUser('gateway')) {
  control.createUser({user: 'gateway', pwd: secret('control_password'), roles: ['readWrite']});
}
const sample = server.getDB('analytics');
if (!sample.getUser('reporter')) {
  sample.createUser({user: 'reporter', pwd: secret('reporter_password'), roles: ['read']});
}
for (const order of [
  {_id: 'order-1001', status: 'paid', amount: 42.50},
  {_id: 'order-1002', status: 'pending', amount: 18.00},
]) {
  sample.orders.updateOne({_id: order._id}, {$setOnInsert: order}, {upsert: true});
}
for (const zone of [
  {_id: 'order-1001', region: 'EU-South'},
  {_id: 'order-1002', region: 'EU-West'},
]) {
  sample.shipping_zones.updateOne({_id: zone._id}, {$setOnInsert: zone}, {upsert: true});
}
sample.orders.createIndex({amount: 1});
