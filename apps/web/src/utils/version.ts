/**
 * 版本号解析与比较公共工具。
 */

export type VersionComparison = -1 | 0 | 1 | null;

/**
 * Pro 组件版本使用“上游版本-修改序号”，比较上游更新时忽略修改序号。
 * 例如 v7.2.92-1 与上游版本比较时按 v7.2.92 处理。
 */
export const getUpstreamBaseVersion = (version?: string | null): string => {
  if (!version) return '';
  const cleaned = version.trim();
  if (!cleaned) return '';
  return cleaned.split('-', 1)[0];
};

export const parseVersionSegments = (version?: string | null): number[] | null => {
  const cleaned = getUpstreamBaseVersion(version).replace(/^v/i, '');
  if (!cleaned) return null;
  const parts = cleaned
    .split(/[^0-9]+/)
    .filter(Boolean)
    .map((segment) => Number.parseInt(segment, 10))
    .filter(Number.isFinite);
  return parts.length ? parts : null;
};

export const compareVersions = (
  latest?: string | null,
  current?: string | null
): VersionComparison => {
  const latestParts = parseVersionSegments(latest);
  const currentParts = parseVersionSegments(current);
  if (!latestParts || !currentParts) return null;
  const length = Math.max(latestParts.length, currentParts.length);
  for (let i = 0; i < length; i++) {
    const l = latestParts[i] || 0;
    const c = currentParts[i] || 0;
    if (l > c) return 1;
    if (l < c) return -1;
  }
  return 0;
};
