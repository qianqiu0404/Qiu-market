-- ============================================
-- 一次性清理：symbol_kline 新旧 guid 格式重复行
-- 背景：K 线 guid 从「{symbol_guid}-{ms}」（旧）改为
-- 「{symbol_guid}-{interval}-{ms}」（新）后，切换窗口内同一根蜡烛
-- 两种 guid 各写一行，前端图表同一时间点出现两个值。
-- 只删除「存在新格式孪生行」的旧格式行（2026-07-22 实测 24 行）；
-- 其余旧格式行是独家历史，不动。
-- 执行：psql -h <host> -U <user> -d s78_market -f script/cleanup-kline-duplicates.sql
-- ============================================

BEGIN;

-- 1. 备份待删行（表不存在才建，可重入）
CREATE TABLE IF NOT EXISTS symbol_kline_dedup_backup_20260722 (LIKE symbol_kline INCLUDING ALL);

INSERT INTO symbol_kline_dedup_backup_20260722
SELECT o.* FROM symbol_kline o
WHERE o.guid ~ '^[^-]+-[0-9]+$'
  AND EXISTS (
    SELECT 1 FROM symbol_kline n
    WHERE n.symbol_guid = o.symbol_guid
      AND n.interval = o.interval
      AND n.created_at = o.created_at
      AND n.guid ~ '^[^-]+-[^-]+-[0-9]+$'
  )
  AND NOT EXISTS (
    SELECT 1 FROM symbol_kline_dedup_backup_20260722 b WHERE b.guid = o.guid
  );

-- 2. 删除重复的旧格式行
DELETE FROM symbol_kline o
WHERE o.guid ~ '^[^-]+-[0-9]+$'
  AND EXISTS (
    SELECT 1 FROM symbol_kline n
    WHERE n.symbol_guid = o.symbol_guid
      AND n.interval = o.interval
      AND n.created_at = o.created_at
      AND n.guid ~ '^[^-]+-[^-]+-[0-9]+$'
  );

-- 3. 验证：应返回 0 行（同一 symbol+interval+created_at 不再有重复）
SELECT symbol_guid, interval, created_at, COUNT(*)
FROM symbol_kline
GROUP BY symbol_guid, interval, created_at
HAVING COUNT(*) > 1
LIMIT 10;

COMMIT;
