-- +goose Up
-- +goose StatementBegin
-- Per-metric sync watermarks.
--
-- neyrox_account.last_sync is a single high-water mark across every metric, which
-- silently drops any metric that is recorded earlier than it is uploaded: sleep is
-- measured overnight but arrives in the morning, so its date_device is always older
-- than the pulse rows already pushed, and it would never pass the filter. Each metric
-- now advances its own watermark; last_sync remains the floor a metric is seeded with
-- the first time it is synced.
create table neyrox_metric_sync
(
    contract_id integer     not null references neyrox_account (contract_id) on delete cascade,
    metric      varchar(64) not null,
    last_sync   timestamp,
    primary key (contract_id, metric)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table neyrox_metric_sync;
-- +goose StatementEnd
