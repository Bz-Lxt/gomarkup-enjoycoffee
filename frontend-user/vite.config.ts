import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { fileURLToPath, URL } from 'node:url';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  // 开发态镜像生产态的同源代理，让两边的请求路径完全一致。
  // 否则"开发能跑、构建后 CORS 失败"这类问题只会在最后一刻暴露。
  // 目标是宿主机映射端口，因为 vite 跑在容器外。
  server: {
    host: true,
    port: 31411,
    strictPort: true,
    proxy: {
      '/api': {
        target: 'http://localhost:31410',
        changeOrigin: true,
        ws: true,
      },
    },
  },
  preview: {
    host: true,
    port: 31411,
    strictPort: true,
    proxy: {
      '/api': {
        target: 'http://localhost:31410',
        changeOrigin: true,
        ws: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
    // 图表代码与业务页面分开打包：图表模块体积较大且几乎不变动，
    // 分开后业务迭代不会让用户重新下载图表代码。
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('node_modules')) {
            if (id.includes('react-router')) return 'router';
            return 'vendor';
          }
          if (id.includes('/src/charts/')) return 'charts';
          return undefined;
        },
      },
    },
  },
});
