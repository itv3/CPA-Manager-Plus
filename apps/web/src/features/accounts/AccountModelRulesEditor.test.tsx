import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { describe, expect, it, vi } from 'vitest';
import { AccountModelRulesEditor } from './AccountModelRulesEditor';

const textOf = (value: unknown): string => {
  if (typeof value === 'string' || typeof value === 'number') return String(value);
  if (Array.isArray(value)) return value.map(textOf).join('');
  if (value && typeof value === 'object' && 'props' in value) {
    return textOf((value as { props: { children?: unknown } }).props.children);
  }
  return '';
};

const buttonByText = (renderer: ReactTestRenderer, text: string) => {
  const button = renderer.root
    .findAllByType('button')
    .find((candidate) => textOf(candidate.props.children).includes(text));
  if (!button) throw new Error(`未找到按钮：${text}`);
  return button;
};

describe('统一账号模型规则编辑器', () => {
  it('空白名单显示允许全部并支持填入自定义模型', () => {
    let models: string[] = [];
    let renderer!: ReactTestRenderer;

    const render = () => (
      <AccountModelRulesEditor
        models={models}
        onModelsChange={(value) => {
          models = value;
          renderer.update(render());
        }}
        mappingLines=""
        onMappingLinesChange={vi.fn()}
      />
    );

    act(() => {
      renderer = create(render());
    });

    expect(JSON.stringify(renderer.toJSON())).toContain('允许全部模型');

    const manualInput = renderer.root.findByProps({
      placeholder: '输入自定义模型名称，可使用末尾通配符 *',
    });
    act(() => manualInput.props.onChange({ target: { value: 'custom-model' } }));
    act(() => renderer.root.findByProps({ 'aria-label': '填入自定义模型' }).props.onClick());

    expect(models).toEqual(['custom-model']);
  });

  it('白名单以列表展示且逐项可删除', () => {
    let models = ['gpt-5.5', 'gpt-5.6-sol'];
    let renderer!: ReactTestRenderer;

    const render = () => (
      <AccountModelRulesEditor
        models={models}
        onModelsChange={(value) => {
          models = value;
          renderer.update(render());
        }}
        mappingLines=""
        onMappingLinesChange={vi.fn()}
      />
    );

    act(() => {
      renderer = create(render());
    });

    expect(JSON.stringify(renderer.toJSON())).toContain('2 个模型');

    const removeModel = renderer.root.findByProps({ 'aria-label': '移除模型 gpt-5.5' });
    act(() => removeModel.props.onClick());
    expect(models).toEqual(['gpt-5.6-sol']);
  });

  it('提供双同步动作并支持清除所有模型', () => {
    const onSyncBuiltInModels = vi.fn();
    const onSyncUpstreamModels = vi.fn();
    let models = ['gpt-5', 'custom-*'];
    let renderer!: ReactTestRenderer;

    const render = () => (
      <AccountModelRulesEditor
        models={models}
        onModelsChange={(value) => {
          models = value;
          renderer.update(render());
        }}
        mappingLines=""
        onMappingLinesChange={vi.fn()}
        onSyncBuiltInModels={onSyncBuiltInModels}
        onSyncUpstreamModels={onSyncUpstreamModels}
      />
    );

    act(() => {
      renderer = create(render());
    });

    act(() => buttonByText(renderer, '同步最新支持模型').props.onClick());
    expect(onSyncBuiltInModels).toHaveBeenCalledTimes(1);

    act(() => buttonByText(renderer, '同步上游支持的模型').props.onClick());
    expect(onSyncUpstreamModels).toHaveBeenCalledTimes(1);

    act(() => buttonByText(renderer, '清除所有模型').props.onClick());
    expect(models).toEqual([]);
  });

  it('使用结构化输入维护模型映射', () => {
    let mappingLines = '';
    let renderer!: ReactTestRenderer;

    const render = () => (
      <AccountModelRulesEditor
        models={[]}
        onModelsChange={vi.fn()}
        mappingLines={mappingLines}
        onMappingLinesChange={(value) => {
          mappingLines = value;
          renderer.update(render());
        }}
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
