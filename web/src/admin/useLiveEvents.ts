import { useEffect, useState } from 'preact/hooks';

/**
 * Subscribes the dashboard to the server event stream.
 *
 * Returns a counter that increments on every meaningful event; views take it as
 * a dependency and refetch. That keeps the transport in one place instead of
 * every view growing its own polling loop.
 */
export function useLiveEvents(): { connected: boolean; lastEvent: number } {
  const [connected, setConnected] = useState(false);
  const [lastEvent, setLastEvent] = useState(0);

  useEffect(() => {
    let source: EventSource | null = null;
    try {
      source = new EventSource('/api/v1/events');
    } catch {
      return;
    }

    const bump = () => setLastEvent((n) => n + 1);

    source.onopen = () => setConnected(true);
    // EventSource retries on its own with backoff, so there is no reconnect
    // logic here to get wrong; the flag simply reflects the current state.
    source.onerror = () => setConnected(false);

    source.addEventListener('hello', () => setConnected(true));
    source.addEventListener('ping', () => setConnected(true));

    for (const name of ['playlist', 'schedule', 'emergency', 'website', 'social', 'player', 'media']) {
      source.addEventListener(name, bump);
    }

    return () => {
      source?.close();
      setConnected(false);
    };
  }, []);

  return { connected, lastEvent };
}
