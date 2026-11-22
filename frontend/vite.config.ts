import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";

// Builds into ../web/dist, which the Go binary embeds. `npm run dev`
// proxies the API to a local `ptz --mock` on :8080 (JADECK_API picks another).
const api = process.env.JADECK_API ?? "127.0.0.1:8080";
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  build: { outDir: "../web/dist", emptyOutDir: true, assetsDir: "assets", chunkSizeWarningLimit: 900 },
  server: {
    proxy: {
      "/api": `http://${api}`,
      "/ws": { target: `ws://${api}`, ws: true },
      "/mcp": `http://${api}`,
    },
  },
});
