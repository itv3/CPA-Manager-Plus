import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { describe, expect, it, vi } from 'vitest';
import { AccountModelRulesEditor } from './AccountModelRulesEditor';

describe('统一账号模型规则编辑器', () => {
  it('支持选择、移除和手工添加模型', () => {
    let selectedModels = ['gpt-5'];
    let manualModels = '';
    let renderer!: ReactTestRenderer;

    const render = () => (
      <AccountModelRulesEditor
        allowAll={false}
        onAllowAllChange={vi.fn()}
        discoveredModels={['gpt-5', 'gpt-5.1']}
        selectedModels={selectedModels}
        onSelectedModelsChange={(value) => {
          selectedModels = value;
          renderer.update(render());
        }}
        manualModels={manualModels}
        onManualModelsChange={(value) => {
          manualModels = value;
          renderer.update(render());
        }}
        mappingLines=""
        onMappingLinesChange={vi.fn()}
        testModel="gpt-5"
        onTestModelChange={vi.fn()}
      />
    );

    act(() => {
      renderer = create(render());
    });

    const availableModel = renderer.root
      .findAllByType('button')
      .find((button) => button.props.children?.[0] === 'gpt-5.1');
    expect(availableModel).toBeDefined();
    act(() => availableModel?.props.onClick());
    expect(selectedModels).toEqual(['gpt-5', 'gpt-5.1']);

    const removeModel = renderer.root.findByProps({ 'aria-label': '移除模型 gpt-5' });
    act(() => removeModel.props.onClick());
    expect(selectedModels).toEqual(['gpt-5.1']);

    const manualInput = renderer.root.findByProps({
      placeholder: '输入模型名称，可使用末尾通配符 *',
    });
    act(() => manualInput.props.onChange({ target: { value: 'custom-*' } }));
    const addButton = renderer.root.findByProps({ 'aria-label': '添加手工模型' });
    act(() => addButton.props.onClick());
    expect(manualModels).toBe('custom-*');
  });

  it('使用结构化输入维护模型映射', () => {
    let mappingLines = '';
    let renderer!: ReactTestRenderer;

    const render = () => (
      <AccountModelRulesEditor
        allowAll
        onAllowAllChange={vi.fn()}
        discoveredModels={[]}
        selectedModels={[]}
        onSelectedModelsChange={vi.fn()}
        manualModels=""
        onManualModelsChange={vi.fn()}
        mappingLines={mappingLines}
        onMappingLinesChange={(value) => {
          mappingLines = value;
          renderer.update(render());
        }}
        testModel="gpt-5"
        onTestModelChange={vi.fn()}
      />
    );

    act(() => {
      renderer = create(render());
    });

    const mappingTab = renderer.root.findByProps({ id: 'segmented-tabs-mapping' });
    act(() => mappingTab.props.onClick({ preventDefault: vi.fn() }));

    const aliasInput = renderer.root.findByProps({ 'aria-label': '第 1 条客户端模型名' });
    act(() => aliasInput.props.onChange({ target: { value: 'fast' } }));
    const upstreamInput = renderer.root.findByProps({ 'aria-label': '第 1 条上游模型名' });
    act(() => upstreamInput.props.onChange({ target: { value: 'gpt-5' } }));
    expect(mappingLines).toBe('fast=gpt-5');
  });
});
