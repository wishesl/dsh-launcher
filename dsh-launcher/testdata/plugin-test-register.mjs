/**
 * 测试专用 ESM 解析钩子的注册入口（`node --import ./testdata/plugin-test-register.mjs`）。
 *
 * 为什么需要：插件的真代码 `import z from "@deepseek-ai/schemastery"`，而仓库里
 * `dsh-launcher/` 下没有给插件用的 node_modules —— 不挂钩子，真插件代码在测试里
 * 根本 import 不进来。只影响这条 `--import` 命令行，产品装载路径完全不受影响。
 */
import { register } from "node:module";

register("./plugin-test-hooks.mjs", import.meta.url);
