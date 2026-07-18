import { useEffect, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { SegmentedTabs } from '@/components/ui/SegmentedTabs';
import { IconPlus, IconRefreshCw, IconTrash2, IconX } from '@/components/ui/icons';
import styles from './AccountModals.module.scss';

interface AccountModelRulesEditorProps {
  models: string[];
  onModelsChange: (value: string[]) => void;
  mappingLines: string;
  onMappingLinesChange: (value: string) => void;
  onSyncBuiltInModels?: () => void;
  onSyncUpstreamModels?: () => void;
  syncingBuiltIn?: boolean;
  syncingUpstream?: boolean;
}

type ModelRuleTab = 'whitelist' | 'mapping';

interface MappingRow {
  alias: string;
  upstream: string;
}

const splitLines = (value: string) =>
  value
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean);

const mappingRowsFromLines = (value: string): MappingRow[] => {
  const rows = value
    .split('\n')
    .map((line) => {
      const separator = line.indexOf('=');
      if (separator < 0) return { alias: line.trim(), upstream: '' };
      return {
        alias: line.slice(0, separator).trim(),
        upstream: line.slice(separator + 1).trim(),
      };
    })
    .filter((row) => row.alias || row.upstream);
  return rows.length > 0 ? rows : [{ alias: '', upstream: '' }];
};

const serializeMappingRows = (rows: MappingRow[]) =>
  rows
    .filter((row) => row.alias || row.upstream)
    .map((row) => `${row.alias}=${row.upstream}`)
    .join('\n');

export function AccountModelRulesEditor({
  models,
  onModelsChange,
  mappingLines,
  onMappingLinesChange,
  onSyncBuiltInModels,
  onSyncUpstreamModels,
  syncingBuiltIn = false,
  syncingUpstream = false,
}: AccountModelRulesEditorProps) {
  const [activeTab, setActiveTab] = useState<ModelRuleTab>('whitelist');
  const [manualDraft, setManualDraft] = useState('');
  const [mappingRows, setMappingRows] = useState<MappingRow[]>(() =>
    mappingRowsFromLines(mappingLines)
  );

  useEffect(() => {
    setMappingRows(mappingRowsFromLines(mappingLines));
  }, [mappingLines]);

  const removeModel = (model: string) => {
    onModelsChange(models.filter((item) => item !== model));
  };

  const appendManualModels = () => {
    const additions = splitLines(manualDraft);
    if (additions.length === 0) return;
    const seen = new Set(models.map((item) => item.toLowerCase()));
    const merged = [...models];
    additions.forEach((item) => {
      const key = item.toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);
      merged.push(item);
    });
    onModelsChange(merged);
    setManualDraft('');
  };

  const updateMappingRow = (index: number, field: keyof MappingRow, value: string) => {
    const next = mappingRows.map((row, rowIndex) =>
      rowIndex === index ? { ...row, [field]: value } : row
    );
    setMappingRows(next);
    onMappingLinesChange(serializeMappingRows(next));
  };

  const removeMappingRow = (index: number) => {
    const next = mappingRows.filter((_, rowIndex) => rowIndex !== index);
    setMappingRows(next.length > 0 ? next : [{ alias: '', upstream: '' }]);
    onMappingLinesChange(serializeMappingRows(next));
  };

  return (
    <div className={styles.modelEditor}>
      <div className={styles.modelEditorHeader}>
        <div>
          <h3 className={styles.sectionTitle}>模型限制（可选）</h3>
          <p className={styles.sectionDescription}>
            列表为空时允许全部模型；同步或填入模型后仅允许列表中的模型。
          </p>
        </div>
      </div>

      <SegmentedTabs
        items={[
          { id: 'whitelist', label: '模型白名单' },
          { id: 'mapping', label: '模型映射' },
        ]}
        activeTab={activeTab}
        ariaLabel="模型规则类型"
        onChange={setActiveTab}
        fullWidth
        equalWidth
      />

      {activeTab === 'whitelist' ? (
        <div className={styles.modelTabPanel}>
          {models.length > 0 ? (
            <div className={styles.modelListBox}>
              <div className={styles.modelListGrid}>
                {models.map((model) => (
                  <span className={styles.modelListItem} key={model}>
                    <span className={styles.modelListName}>{model}</span>
                    <button
                      type="button"
                      onClick={() => removeModel(model)}
                      aria-label={`移除模型 ${model}`}
                    >
                      <IconX size={14} />
                    </button>
                  </span>
                ))}
              </div>
              <div className={styles.modelListCount}>{`${models.length} 个模型`}</div>
            </div>
          ) : (
            <div className={styles.modelAllNotice}>
              尚未选择模型，当前允许全部模型。可点击下方按钮同步，或直接填入模型名称。
            </div>
          )}

          <div className={styles.modelSyncActions}>
            {onSyncBuiltInModels ? (
              <Button
                variant="secondary"
                size="xs"
                onClick={onSyncBuiltInModels}
                loading={syncingBuiltIn}
              >
                <IconRefreshCw size={14} /> 同步最新支持模型
              </Button>
            ) : null}
            {onSyncUpstreamModels ? (
              <Button
                variant="secondary"
                size="xs"
                onClick={onSyncUpstreamModels}
                loading={syncingUpstream}
              >
                <IconRefreshCw size={14} /> 同步上游支持的模型
              </Button>
            ) : null}
            <Button
              variant="secondary"
              size="xs"
              onClick={() => onModelsChange([])}
              disabled={models.length === 0}
            >
              <IconTrash2 size={14} /> 清除所有模型
            </Button>
          </div>

          <div className={styles.field}>
            <span className={styles.fieldLabel}>自定义模型名称</span>
            <div className={styles.manualModelRow}>
              <input
                className={styles.input}
                value={manualDraft}
                onChange={(event) => setManualDraft(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key !== 'Enter') return;
                  event.preventDefault();
                  appendManualModels();
                }}
                placeholder="输入自定义模型名称，可使用末尾通配符 *"
              />
              <Button
                variant="secondary"
                size="sm"
                onClick={appendManualModels}
                aria-label="填入自定义模型"
              >
                <IconPlus size={14} /> 填入
              </Button>
            </div>
          </div>
        </div>
      ) : (
        <div className={styles.modelTabPanel}>
          <div className={styles.mappingHeader}>
            <span>客户端模型名</span>
            <span>上游模型名</span>
            <span />
          </div>
          <div className={styles.mappingRows}>
            {mappingRows.map((row, index) => (
              <div className={styles.mappingRow} key={`${index}-${mappingRows.length}`}>
                <input
                  className={styles.input}
                  value={row.alias}
                  onChange={(event) => updateMappingRow(index, 'alias', event.target.value)}
                  placeholder="例如 fast 或 claude-*"
                  aria-label={`第 ${index + 1} 条客户端模型名`}
                />
                <span className={styles.mappingArrow}>→</span>
                <input
                  className={styles.input}
                  value={row.upstream}
                  onChange={(event) => updateMappingRow(index, 'upstream', event.target.value)}
                  placeholder="上游真实模型名"
                  aria-label={`第 ${index + 1} 条上游模型名`}
                />
                <button
                  type="button"
                  className={styles.mappingRemove}
                  onClick={() => removeMappingRow(index)}
                  aria-label={`删除第 ${index + 1} 条模型映射`}
                >
                  <IconTrash2 size={15} />
                </button>
              </div>
            ))}
          </div>
          <Button
            variant="secondary"
            size="xs"
            onClick={() => setMappingRows([...mappingRows, { alias: '', upstream: '' }])}
          >
            <IconPlus size={14} /> 添加映射
          </Button>
          <p className={styles.sectionDescription}>
            客户端模型名允许在末尾使用一个通配符，目标模型不允许使用通配符。
          </p>
        </div>
      )}
    </div>
  );
}
