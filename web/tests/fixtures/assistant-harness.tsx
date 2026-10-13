// Explicit isolated UI fixture. Real backend/runtime tests are separate.
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ConfigProvider } from "antd";
import { AssistantPage } from "../../src/pages/AssistantPage";
import { setCredentials } from "../../src/api/client";
import "../../src/styles.css";
setCredentials("fixture-token", "fixture-tenant");
createRoot(document.getElementById("root")!).render(
  <ConfigProvider>
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <BrowserRouter>
        <main style={{ padding: 16, minWidth: 0 }}>
          <AssistantPage />
        </main>
      </BrowserRouter>
    </QueryClientProvider>
  </ConfigProvider>,
);
