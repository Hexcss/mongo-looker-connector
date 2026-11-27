// Synthetic records and credentials for the disposable Compose fixture.
const analytics = db.getSiblingDB("analytics");
analytics.createRole({
  role: "ordersReader",
  privileges: [{ resource: { db: "analytics", collection: "orders" }, actions: ["find"] }],
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
