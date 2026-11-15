import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/v1": "http://127.0.0.1:18080",
      "/healthz": "http://127.0.0.1:18080",
      "/openapi.json": "http://127.0.0.1:18080",
      "/docs": "http://127.0.0.1:18080",
    },
  },
});
