/**
 * Vitest setup.
 *
 * jsdom lacks a few browser APIs the components touch. Stubbing them here keeps
 * the component tests focused on behaviour rather than environment plumbing.
 */

// EventSource is not implemented in jsdom; a no-op keeps components that open a
// stream from throwing during a render test.
class FakeEventSource {
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  addEventListener(): void {}
  removeEventListener(): void {}
  close(): void {}
}
// @ts-expect-error assigning a test double onto the global
globalThis.EventSource = FakeEventSource;

// matchMedia is consulted by reduced-motion aware code paths.
if (!window.matchMedia) {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
}
