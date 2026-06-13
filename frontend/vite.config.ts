import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { VitePWA } from "vite-plugin-pwa";

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    VitePWA({
      registerType: "prompt",
      includeAssets: ["favicon.svg"],
      manifest: {
        name: "Safe Space",
        short_name: "SafeSpace",
        description: "Chat with AI agents in isolated sandboxes",
        theme_color: "#0f172a",
        background_color: "#0f172a",
        display: "standalone",
        start_url: "/",
        icons: [
          { src: "/favicon.svg", sizes: "any", type: "image/svg+xml" },
        ],
      },
      workbox: {
        // Precache only core app chunks (index, vendor, query, CSS, HTML, SVG).
        // Shiki language grammar chunks are excluded and handled by runtimeCaching
        // below. This prevents force-downloading all grammar files at SW install time.
        // Update these patterns when manualChunks config changes.
        globPatterns: ["**/*.{css,html,svg}", "**/index*.js", "**/vendor*.js", "**/query*.js"],
        navigateFallback: "/index.html",
        navigateFallbackDenylist: [/^\/api/],
        runtimeCaching: [
          {
            urlPattern: /^\/env\.json$/,
            handler: "NetworkFirst",
            options: { cacheName: "env-config", expiration: { maxEntries: 1 } },
          },
          {
            // Shiki language grammar chunks and other async JS chunks:
            // cache on first use (CacheFirst), evict LRU after 50 entries or 30 days.
            // Content-addressed filenames (Vite hashes) make CacheFirst safe.
            urlPattern: ({ url }: { url: URL }) =>
              url.pathname.startsWith("/assets/") && url.pathname.endsWith(".js"),
            handler: "CacheFirst",
            options: {
              cacheName: "async-chunks",
              expiration: {
                maxEntries: 50,
                maxAgeSeconds: 30 * 24 * 60 * 60,
              },
            },
          },
        ],
      },
    }),
  ],
  server: {
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: true,
      },
    },
  },
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes("node_modules/react-dom") || id.includes("node_modules/react/") || id.includes("node_modules/react-router")) {
            return "vendor";
          }
          if (id.includes("node_modules/@tanstack/react-query")) {
            return "query";
          }
        },
      },
    },
  },
});
