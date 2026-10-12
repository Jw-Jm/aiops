import ReactDOM from "react-dom/client";
import { RouterProvider } from "react-router-dom";
import { ConfigProvider, App } from "antd";
import zhCN from "antd/locale/zh_CN";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AuthProvider } from "./auth/AuthProvider";
import { authEvents } from "./api/client";
import { router } from "./app/router";
import { tokens } from "./design/tokens";
import "./styles.css";
const client = new QueryClient({
  defaultOptions: {
    queries: { retry: false, staleTime: 15000, refetchOnWindowFocus: true },
    mutations: { retry: false },
  },
});
authEvents.addEventListener("expired", () => client.clear());
authEvents.addEventListener("authorizationChanged", () => {
  void client.cancelQueries();
  client.clear();
});
ReactDOM.createRoot(document.getElementById("root")!).render(
  <ConfigProvider locale={zhCN} theme={{ token: tokens }}>
    <App>
      <QueryClientProvider client={client}>
        <AuthProvider>
          <RouterProvider router={router} />
        </AuthProvider>
      </QueryClientProvider>
    </App>
  </ConfigProvider>,
);
