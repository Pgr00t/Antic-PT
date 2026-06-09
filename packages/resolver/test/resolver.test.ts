import { describe, it, expect } from "vitest";
import { AnticipationResolver } from '../src/resolver';

describe("AnticipationResolver URL construction", () => {
  it("resolves absolute URLs correctly without prepending base", async () => {
    let passedUrl = "";
    globalThis.fetch = async (url: any, options: any) => {
      passedUrl = url;
      return {
        ok: true,
        json: async () => ({}),
        headers: new Headers()
      } as any;
    };

    const resolverAbsolute = new AnticipationResolver("http://localhost:4002/spec/api/v3/order", {
      baseUrl: "http://localhost:1234"
    });
    
    await resolverAbsolute.fetch();
    expect(passedUrl).toBe("http://localhost:4002/spec/api/v3/order");
  });

  it("resolves relative URLs correctly by prepending base", async () => {
    let passedUrl = "";
    globalThis.fetch = async (url: any, options: any) => {
      passedUrl = url;
      return {
        ok: true,
        json: async () => ({}),
        headers: new Headers()
      } as any;
    };

    const resolverRelative = new AnticipationResolver("/api/v3/order", {
      baseUrl: "http://localhost:1234"
    });
    
    await resolverRelative.fetch();
    expect(passedUrl).toBe("http://localhost:1234/api/v3/order");
  });
});
