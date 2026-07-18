import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { describe, expect, it, vi } from 'vitest';
import { DEFAULT_VISUAL_VALUES } from '@/types/visualConfig';
import { VisualConfigEditor } from './VisualConfigEditor';

vi.mock('react-i18next', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-i18next')>();
  return {
    ...actual,
    useTranslation: () => ({
      t: (key: string) => key,
    }),
  };
});

vi.mock('@/hooks/useMediaQuery', () => ({
  useMediaQuery: () => false,
}));

describe('VisualConfigEditor', () => {
  it('renders the Pro section last and updates the protocol model list toggle', () => {
    const onChange = vi.fn();
    let renderer!: ReactTestRenderer;

    act(() => {
      renderer = create(<VisualConfigEditor values={DEFAULT_VISUAL_VALUES} onChange={onChange} />);
    });

    const toggle = renderer.root.findByProps({
      'aria-label': 'config_management.visual.sections.pro.protocol_model_list_enabled',
    });
    expect(toggle.props.checked).toBe(false);

    const sectionIds = renderer.root
      .findAllByType('section')
      .map((section) => section.props.id)
      .filter(Boolean);
    expect(sectionIds[sectionIds.length - 1]).toBe('pro');

    act(() => {
      toggle.props.onChange({ target: { checked: true } });
    });
    expect(onChange).toHaveBeenCalledWith({ protocolModelListEnabled: true });

    act(() => {
      renderer.unmount();
    });
  });
});
