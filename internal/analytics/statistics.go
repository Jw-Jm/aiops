package analytics

// Scope is identical to count datasets. A single statement snapshot produces
// both the statistic and coverage; invalid durations are never coerced to zero.
const statisticFilter = `), filtered AS (SELECT * FROM scoped WHERE ($7::text='' OR namespace=$7) AND ($8::text='' OR state=$8) AND ($9::text='' OR severity=$9) AND ($10::text='' OR operation=$10) AND ($11::text='' OR subject=$11)), buckets AS (
 SELECT *,CASE $12::text WHEN 'namespace' THEN namespace WHEN 'day' THEN to_char(event_time AT TIME ZONE $13,'YYYY-MM-DD') ELSE '' END AS dimension
 FROM filtered WHERE event_time>=$14 AND event_time<$15), grouped AS (`

const recoverySuffix = statisticFilter + `SELECT dimension,avg(sample)::double precision AS value FROM buckets WHERE sample IS NOT NULL GROUP BY dimension), coverage AS (SELECT count(*)::bigint AS missing FROM filtered WHERE event_time IS NULL OR (event_time>=$14 AND event_time<$15 AND sample IS NULL))
 SELECT false AS metadata,dimension,value,coverage.missing,pg_current_snapshot()::text FROM grouped CROSS JOIN coverage
 UNION ALL SELECT true,'',0::double precision,coverage.missing,pg_current_snapshot()::text FROM coverage`

const successSuffix = statisticFilter + `SELECT dimension,(100.0*count(*) FILTER(WHERE state='succeeded')/count(*))::double precision AS value FROM buckets GROUP BY dimension), coverage AS (SELECT count(*)::bigint AS missing FROM filtered WHERE event_time IS NULL)
 SELECT false AS metadata,dimension,value,coverage.missing,pg_current_snapshot()::text FROM grouped CROSS JOIN coverage
 UNION ALL SELECT true,'',0::double precision,coverage.missing,pg_current_snapshot()::text FROM coverage`
