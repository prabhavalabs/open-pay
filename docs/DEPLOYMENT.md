# Production Deployment (shared host)

Production runs `docker-compose.prod.yml` through `./deploy.sh production`
on a host that other projects share. Open Pay bundles only Redis and NATS.
Postgres and object storage come from the host's shared platform stack:

| Service | Container | Reached at |
|---|---|---|
| Postgres | `platform-postgres` | `platform-postgres:5432` |
| S3 (Garage) | `platform-garage` | `http://platform-garage:3900`, region `garage`, path-style |

Both are on the external Docker network `platform` (override the name with
`PLATFORM_NETWORK`). Only the services that use a database or the object
store join it; the gateway, Redis and NATS stay on the private `openpay-net`
network. `deploy.sh` stops early when the platform network is missing.

## Databases

On a shared server generic names such as `payment_db` would be ambiguous, so
every database is prefixed and owned by one non-superuser role:

| Service | Local development | Production |
|---|---|---|
| merchant | `merchant_db` | `openpay_merchant` |
| payment | `payment_db` | `openpay_payment` |
| settlement | `settlement_db` | `openpay_settlement` |
| webhook | `webhook_db` | `openpay_webhook` |
| subscription | `subscription_db` | `openpay_subscription` |
| notification | `notification_db` | `openpay_notification` |
| exchange | `exchange_db` | `openpay_exchange` |
| admin | `admin_db` | `openpay_admin` |
| directdebit | `directdebit_db` | `openpay_directdebit` |
| (none; default database) | `olp_default` | `openpay_default` |

Services and the migrate container build their connection from:

| Variable | Production value | Notes |
|---|---|---|
| `DB_HOST` | `platform-postgres` | |
| `DB_PORT` | `5432` | |
| `DB_USER` | `openpay` | Required |
| `DB_PASSWORD` | | Required. Keep it URL-safe (`openssl rand -hex 24`); migrations put it in a URL |
| `DB_NAME_PREFIX` | `openpay_` | Required |
| `DB_NAME_SUFFIX` | empty | |
| `DB_SSLMODE` | `disable` | Traffic stays on the Docker network |
| `DB_POOL_MAX_CONNS` | `5` | Per service; nine services share the role's connection limit |

Nothing in the migrations needs a superuser: there is no `CREATE EXTENSION`
(`gen_random_uuid()` is built into Postgres 13+), and the services never
create databases at runtime. The databases must exist before the first
deploy, created by a server administrator:

```sql
CREATE ROLE openpay LOGIN PASSWORD '<password>' CONNECTION LIMIT 60;
CREATE DATABASE openpay_payment OWNER openpay;
-- ...repeat for every database in the table above...
REVOKE ALL ON DATABASE openpay_payment FROM PUBLIC;
```

## Object storage

| Variable | Production value | Notes |
|---|---|---|
| `S3_ENDPOINT` | `http://platform-garage:3900` | `host:port` or URL; `https://` turns on TLS |
| `S3_REGION` | `garage` | Set it so the client skips region discovery |
| `S3_FORCE_PATH_STYLE` | `true` | |
| `S3_USE_SSL` | unset | Overrides the endpoint scheme |
| `S3_ACCESS_KEY_ID` | | Required. A key scoped to the bucket, not an admin login |
| `S3_SECRET_ACCESS_KEY` | | Required |
| `S3_BUCKET` | `openpay-kyc-documents` | Required |

The bucket holds KYC documents and must stay private: no anonymous read
policy and no website access. Browsers never fetch objects from the store.
Uploads return the gateway path `/v1/assets/<key>`, which the admin service
streams from the bucket. Create the bucket and the key before deploying; the
services create a missing bucket only when the key is allowed to.

## Moving data from the bundled stack

The bundled `postgres` and `minio` services are gone from the compose file,
so the next `deploy.sh` run removes those containers (`--remove-orphans`).
Their named volumes are left in place. Copy the data across first:

1. Dump each old database and restore it into its new name, connected as
   `openpay`, with `pg_restore --no-owner --no-privileges`, e.g. `payment_db`
   into `openpay_payment`. The `schema_migrations` table comes along, so the
   deploy's migration step only applies newer migrations.
2. Copy the objects of the old `kyc-documents` bucket into
   `openpay-kyc-documents`, keeping the object keys unchanged; the keys are
   stored in the merchant database.
3. Update the env file (see `.env.prod.example`), then deploy.
