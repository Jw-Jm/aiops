# Frozen Querier contract fixture

This is a synthetic, frozen API contract fixture for DeepFlow v7.2.0, commit e567b167453ffa99f08f26def20379b4f831e073. It is not a live capture. Runtime capability remains `fixture_only`; L7 is disabled and Trace is `CAPABILITY_DISABLED`.

The response envelope and column ordering follow the Server/Querier API. Numeric status values are checked against `server/querier/db_descriptions/clickhouse/tag/enum/status.en`; observation points follow `enum/observation_point.en`. `toUnixTimestamp(time)` is supported by the pinned Querier expression translation fixtures. Status 3 represents server error and retransmission is supporting evidence for packet loss, never a direct packet-loss measurement. Path output is explicitly incomplete because this fixture contains only an L4 observation, not verified hop topology.
