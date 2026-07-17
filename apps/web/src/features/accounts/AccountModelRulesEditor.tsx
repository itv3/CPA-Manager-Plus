import { useEffect, useMemo, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { SegmentedTabs } from '@/components/ui/SegmentedTabs';
import { IconPlus, IconRefreshCw, IconTrash2, IconX } from '@/components/ui/icons';
import styles from './AccountModals.module.scss';

interface AccountModelRulesEditorProps {
  allowAll: boolean;
  onAllowAllChange: (value: boolean) => void;
  discoveredModels: string[];
  selectedModels: string[];
  onSelectedModelsChange: (value: string[]) => void;
  manualModels: string;
  onManualModelsChange: (value: string) => void;
  mappingLines: string;
  onMappingLinesChange: (value: string) => void;
  testModel: string;
  onTestModelChange: (value: string) => void;
  onSyncModels?: () => void;
  syncingModels?: boolean;
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
  allowAll,
  onAllowAllChange,
  discoveredModels,
  selectedModels,
  onSelectedModelsChange,
  manualModels,
  onManualModelsChange,
  mappingLines,
  onMappingLinesChange,
  testModel,
  onTestModelChange,
  onSyncModels,
  syncingModels = false,
}: AccountModelRulesEditorProps) {
  const [activeTab, setActiveTab] = useState<ModelRuleTab>('whitelist');
  const [manualDraft, setManualDraft] = useState('');
  const manualEntries = useMemo(() => splitLines(manualModels), [manualModels]);
  const [mappingRows, setMappingRows] = useState<MappingRow[]>(() =>
    mappingRowsFromLines(mappingLines)
  );
  const selectedCount = allowAll
    ? discoveredModels.length
    : selectedModels.length + manualEntries.length;

  useEffect(() => {
    setMappingRows(mappingRowsFromLines(mappingLines));
  }, [mappingLines]);

  const toggleModel = (model: string) => {
    if (selectedModels.includes(model)) {
      onSelectedModelsChange(selectedModels.filter((item) => item !== model));
      return;
    }
    onSelectedModelsChange([...selectedModels, model]);
  };

  const removeManualModel = (model: string) => {
    onManualModelsChange(manualEntries.filter((item) => item !== model).join('\n'));
  };

  const appendManualModel = () => {
    const additions = splitLines(manualDraft);
    if (additions.length === 0) return;
    onManualModelsChange([...new Set([...manualEntries, ...additions])].join('\n'));
    setManualDraft('');
    onAllowAllChange(false);
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
          <h3 className={styles.sectionTitle}>模型限制</h3>
          <p className={styles.sectionDescription}>
            选择账号允许调用的模型，并按需配置客户端别名到上游模型的映射。
          </p>
        </div>
        <div className={styles.inlineActions}>
          {onSyncModels ? (
            <Button variant="secondary" size="xs" onClick={onSyncModels} loading={syncingModels}>
              <IconRefreshCw size={14} /> 同步最新模型
            </Button>
          ) : null}
          <Button
            variant="secondary"
            size="xs"
            onClick={() => {
              onSelectedModelsChange([]);
              onManualModelsChange('');
              onAllowAllChange(true);
            }}
          >
            <IconTrash2 size={14} /> 清空白名单
          </Button>
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
          <div className={styles.allowMode} role="group" aria-label="模型允许范围">
            <button
              type="button"
              className={allowAll ? styles.allowModeActive : ''}
              onClick={() => onAllowAllChange(true)}
            >
              允许全部模型
            </button>
            <button
              type="button"
              className={!allowAll ? styles.allowModeActive : ''}
              onClick={() => onAllowAllChange(false)}
            >
              使用模型白名单
            </button>
          </div>

          {!allowAll ? (
            <>
              <div className={styles.field}>
                <span className={styles.fieldLabel}>已选择模型（{selectedCount}）</span>
                <div className={styles.selectedModelList}>
                  {selectedModels.map((model) => (
                    <span className={styles.selectedModelChip} key={model}>
                      {model}
                      <button
                        type="button"
                        onClick={() => toggleModel(model)}
                        aria-label={`移除模型 ${model}`}
                      >
                        <IconX size={12} />
                      </button>
                    </span>
                  ))}
                  {manualEntries.map((model) => (
                    <span className={styles.selectedModelChip} key={`manual-${model}`}>
                      {model}
                      <button
                        type="button"
                        onClick={() => removeManualModel(model)}
                        aria-label={`移除手工模型 ${model}`}
                      >
                        <IconX size={12} />
                      </button>
                    </span>
                  ))}
                  {selectedCount === 0 ? (
                    <span className={styles.modelEmpty}>尚未选择模型</span>
                  ) : null}
                </div>
              </div>

              {discoveredModels.length > 0 ? (
                <div className={styles.field}>
                  <span className={styles.fieldLabel}>可用模型</span>
                  <div className={styles.modelChoices}>
                    {discoveredModels.map((model) => {
                      const selected = selectedModels.includes(model);
                      return (
                        <button
                          key={model}
                          type="button"
                          className={`${styles.modelChoice} ${selected ? styles.modelChoiceSelected : ''}`}
                          onClick={() => toggleModel(model)}
                          aria-pressed={selected}
                        >
                          {model}
                          {selected ? <span>已选</span> : null}
                        </button>
                      );
                    })}
                  </div>
                </div>
              ) : (
                <div className={styles.notice}>未同步到模型目录，可在下方手工添加模型。</div>
              )}

              <div className={styles.field}>
                <span className={styles.fieldLabel}>手工添加模型</span>
                <div className={styles.manualModelRow}>
                  <input
                    className={styles.input}
                    value={manualDraft}
                    onChange={(event) => setManualDraft(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key !== 'Enter') return;
                      event.preventDefault();
                      appendManualModel();
                    }}
                    placeholder="输入模型名称，可使用末尾通配符 *"
                  />
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={appendManualModel}
                    aria-label="添加手工模型"
                  >
                    <IconPlus size={14} /> 添加
                  </Button>
                </div>
              </div>
            </>
          ) : (
            <div className={styles.modelAllNotice}>
              当前账号允许使用同步到的全部模型。后续新增模型也会自动允许。
            </div>
          )}
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

      <label className={styles.field}>
        <span className={styles.fieldLabel}>连通性测试模型</span>
        <input
          className={styles.input}
          value={testModel}
          onChange={(event) => onTestModelChange(event.target.value)}
          placeholder="选择一个有效的客户端模型名"
        />
        <span className={styles.fieldHint}>保存前会使用该模型发起真实连通性测试。</span>
      </label>
    </div>
  );
}
