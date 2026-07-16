import { SelectionCheckbox } from '@/components/ui/SelectionCheckbox';
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
}

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
}: AccountModelRulesEditorProps) {
  const toggleModel = (model: string, checked: boolean) => {
    if (checked) {
      onSelectedModelsChange([...new Set([...selectedModels, model])]);
      return;
    }
    onSelectedModelsChange(selectedModels.filter((item) => item !== model));
  };

  return (
    <div className={styles.formStack}>
      <SelectionCheckbox
        checked={allowAll}
        onChange={onAllowAllChange}
        label="允许全部模型"
        ariaLabel="允许全部模型"
      />

      {!allowAll && discoveredModels.length > 0 ? (
        <div className={styles.field}>
          <span className={styles.fieldLabel}>探测到的模型</span>
          <div className={styles.modelChoices}>
            {discoveredModels.map((model) => (
              <SelectionCheckbox
                key={model}
                checked={selectedModels.includes(model)}
                onChange={(checked) => toggleModel(model, checked)}
                label={model}
                ariaLabel={`允许模型 ${model}`}
              />
            ))}
          </div>
        </div>
      ) : null}

      {!allowAll ? (
        <label className={styles.field}>
          <span className={styles.fieldLabel}>手工模型</span>
          <textarea
            className={styles.textarea}
            value={manualModels}
            onChange={(event) => onManualModelsChange(event.target.value)}
            rows={4}
            placeholder="每行一个模型，可在末尾使用 *"
          />
        </label>
      ) : null}

      <label className={styles.field}>
        <span className={styles.fieldLabel}>模型别名与映射</span>
        <textarea
          className={styles.textarea}
          value={mappingLines}
          onChange={(event) => onMappingLinesChange(event.target.value)}
          rows={4}
          placeholder="客户端别名=上游模型"
        />
      </label>

      <label className={styles.field}>
        <span className={styles.fieldLabel}>连通性测试模型</span>
        <input
          className={styles.input}
          value={testModel}
          onChange={(event) => onTestModelChange(event.target.value)}
          placeholder="选择一个有效的客户端模型名"
        />
      </label>
    </div>
  );
}
