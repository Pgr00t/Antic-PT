/**
 * SSEParser parses a Stream of text/event-stream data into structured events.
 * It is designed to work with the ReadableStream from modern fetch API.
 */
export interface SSEEvent {
  event: string;
  id: string;
  data: string;
}

export type EventCallback = (event: SSEEvent) => void;

export class SSEParser {
  private buffer: string = '';
  private onEvent: EventCallback;

  constructor(onEvent: EventCallback) {
    this.onEvent = onEvent;
  }

  /**
   * Push a chunk of data (typically from a Uint8Array) into the parser.
   */
  push(chunk: string) {
    this.buffer += chunk;
    const lines = this.buffer.split(/\r?\n/);
    
    // Keep the last partial line in the buffer.
    this.buffer = lines.pop() || '';

    let currentEvent: Partial<SSEEvent> = {};

    for (const line of lines) {
      if (line.trim() === '') {
        // Empty line signals the end of an event block.
        if (currentEvent.event || currentEvent.id || currentEvent.data) {
          this.onEvent({
            event: currentEvent.event || 'message',
            id: currentEvent.id || '',
            data: currentEvent.data || '',
          });
          currentEvent = {};
        }
        continue;
      }

      const colonIndex = line.indexOf(':');
      if (colonIndex === -1) continue;

      const field = line.slice(0, colonIndex).trim();
      let value = line.slice(colonIndex + 1);
      if (value.startsWith(' ')) value = value.slice(1);

      switch (field) {
        case 'event':
          currentEvent.event = value;
          break;
        case 'id':
          currentEvent.id = value;
          break;
        case 'data':
          // SSE supports multiple data lines, but Antic-PT uses single-line JSON mostly.
          currentEvent.data = currentEvent.data ? currentEvent.data + '\n' + value : value;
          break;
      }
    }
  }
}
