# Foya Website

Foya 官网和文档站，基于 Astro 与 Starlight。

官网页面位于 `src/pages/`，文档源码位于 `src/content/docs/`。

```bash
pnpm install
pnpm dev
```

提交前检查文档元数据、站内链接和 Astro 页面：

```bash
pnpm check
```

生产构建会先执行同一组检查：

```bash
pnpm build
pnpm preview
```

部署到子路径时，通过环境变量覆盖地址：

```bash
SITE_URL=https://freesoulcode.github.io BASE_PATH=/foya pnpm build
```
