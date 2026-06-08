import { AnticipationResolver } from '../src/resolver';

async function runTests() {
  let passedUrl = "";
  
  // Mock global fetch to capture the URL
  globalThis.fetch = async (url: any, options: any) => {
    passedUrl = url;
    return {
      ok: true,
      json: async () => ({}),
      headers: new Headers()
    } as any;
  };

  // 1. Test Absolute URL (Regression Test for Network Error Bug)
  const resolverAbsolute = new AnticipationResolver("http://localhost:4002/spec/api/v3/order", {
    baseUrl: "http://localhost:1234" // Simulate browser window.location.origin
  });
  
  await resolverAbsolute.fetch();
  
  if (passedUrl !== "http://localhost:4002/spec/api/v3/order") {
    console.error(`❌ Regression Test Failed! Expected absolute URL, got: ${passedUrl}`);
    process.exit(1);
  }

  // 2. Test Relative URL
  const resolverRelative = new AnticipationResolver("/api/v3/order", {
    baseUrl: "http://localhost:1234"
  });
  
  await resolverRelative.fetch();
  
  if (passedUrl !== "http://localhost:1234/api/v3/order") {
    console.error(`❌ Relative Test Failed! Expected concatenated URL, got: ${passedUrl}`);
    process.exit(1);
  }

  console.log("✅ AnticipationResolver URL construction tests passed!");
}

runTests().catch(e => {
  console.error(e);
  process.exit(1);
});
