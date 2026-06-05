export class SSEConnection {
  private stream: TransformStream;
  private writer: WritableStreamDefaultWriter;

  constructor() {
    this.stream = new TransformStream();
    this.writer = this.stream.writable.getWriter();
  }

  public getResponse(headers: HeadersInit = {}): Response {
    return new Response(this.stream.readable, {
      headers: {
        "Content-Type": "text/event-stream",
        "Cache-Control": "no-cache",
        Connection: "keep-alive",
        "Access-Control-Allow-Origin": "*",
        ...headers,
      },
    });
  }

  public async send(event: string, data: any, id?: string): Promise<void> {
    const encoder = new TextEncoder();
    let payload = "";

    if (id) payload += `id: ${id}\n`;
    payload += `event: ${event}\n`;
    payload += `data: ${JSON.stringify(data)}\n\n`;

    await this.writer.write(encoder.encode(payload));
  }

  public async close(): Promise<void> {
    await this.writer.close();
  }
}
