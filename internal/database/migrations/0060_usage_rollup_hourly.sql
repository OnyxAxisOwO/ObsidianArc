-- Hourly usage rollups for analytics and leaderboards.
--
-- Heavy aggregates over usage_records (rankings, usage boards, time series,
-- heatmaps) scale with every turn ever made. Grouped by the UTC hour,
-- account, model, provider, group and status, this table caps query size to a
-- few thousand rows per month regardless of traffic volume.

CREATE TABLE usage_rollup_hourly (
    hour_ts          BIGINT NOT NULL,
    user_id          TEXT NOT NULL,
    model_id         TEXT NOT NULL DEFAULT '',
    provider_id      TEXT NOT NULL DEFAULT '',
    group_id         TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT '',
    requests         BIGINT NOT NULL DEFAULT 0,
    input_tokens     BIGINT NOT NULL DEFAULT 0,
    output_tokens    BIGINT NOT NULL DEFAULT 0,
    reasoning_tokens BIGINT NOT NULL DEFAULT 0,
    total_tokens     BIGINT NOT NULL DEFAULT 0,
    credits          DOUBLE PRECISION NOT NULL DEFAULT 0,
    duration_ms      BIGINT NOT NULL DEFAULT 0,
    errors           BIGINT NOT NULL DEFAULT 0,
    model_name       TEXT NOT NULL DEFAULT '',
    provider_name    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (hour_ts, user_id, model_id, provider_id, group_id, status)
);

CREATE INDEX ix_rollup_hourly_time ON usage_rollup_hourly (hour_ts);
CREATE INDEX ix_rollup_hourly_user ON usage_rollup_hourly (user_id, hour_ts);
CREATE INDEX ix_rollup_hourly_model ON usage_rollup_hourly (model_id, hour_ts);
CREATE INDEX ix_rollup_hourly_provider ON usage_rollup_hourly (provider_id, hour_ts);

INSERT INTO usage_rollup_hourly (
    hour_ts,
    user_id,
    model_id,
    provider_id,
    group_id,
    status,
    requests,
    input_tokens,
    output_tokens,
    reasoning_tokens,
    total_tokens,
    credits,
    duration_ms,
    errors,
    model_name,
    provider_name
)
SELECT
    (started_at - (started_at % 3600000)) AS hour_ts,
    user_id,
    model_id,
    provider_id,
    group_id,
    status,
    COUNT(*) AS requests,
    SUM(input_tokens) AS input_tokens,
    SUM(output_tokens) AS output_tokens,
    SUM(reasoning_tokens) AS reasoning_tokens,
    SUM(total_tokens) AS total_tokens,
    SUM(credits) AS credits,
    SUM(duration_ms) AS duration_ms,
    SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END) AS errors,
    MAX(model_name) AS model_name,
    MAX(provider_name) AS provider_name
FROM usage_records
GROUP BY (started_at - (started_at % 3600000)), user_id, model_id, provider_id, group_id, status;

DROP INDEX IF EXISTS ix_usage_status_time;
DROP INDEX IF EXISTS ix_usage_status_started_at;
CREATE INDEX ix_usage_records_ok_time ON usage_records (started_at) WHERE status = 'ok';
