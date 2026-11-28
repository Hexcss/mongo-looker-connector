// Synthetic records and credentials for the disposable Compose fixture.
const analytics = db.getSiblingDB("analytics");
analytics.createRole({
  role: "ordersReader",
  privileges: ["orders", "shipping_zones"].map(collection => ({ resource: { db: "analytics", collection }, actions: ["find"] })),
  roles: [],
});
analytics.createUser({
  user: "reporter",
  pwd: "local-demo-only",
  roles: [{ role: "ordersReader", db: "analytics" }],
});
analytics.orders.insertMany([
  { _id: "order-001", amount: 42, customer: { city: "Madrid" }, paid: true },
  { _id: "order-002", amount: 17, customer: { city: "Porto" }, paid: false },
]);
analytics.shipping_zones.insertMany([
  { _id: "order-001", region: "EU-South" },
  { _id: "order-002", region: "EU-West" },
]);
analytics.orders.createIndex({ amount: 1 });
