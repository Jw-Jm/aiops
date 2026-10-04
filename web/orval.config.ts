import { defineConfig } from 'orval';

export default defineConfig({
  platform: {
    input: {
      target: '../api/openapi/platform-v1.yaml',
    },
    output: {
      mode: 'single',
      clean: true,
      target: 'src/api/generated/platform.ts',
      schemas: 'src/api/generated/models',
      client: 'fetch',
    },
  },
});
