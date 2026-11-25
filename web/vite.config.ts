import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    // dist/.keep must survive so `go build` works before the UI is built.
    emptyOutDir: false,
    assetsInlineLimit: 0,
  },
  server: {
    proxy: {
      "/api": "http://localhost:8080",
      "/logo": "http://localhost:8080",
    },
  },
});
