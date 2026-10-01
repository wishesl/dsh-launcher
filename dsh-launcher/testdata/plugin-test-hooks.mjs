/**
 * 测试专用模块解析钩子：把插件的 `@deepseek-ai/schemastery` 依赖指到本地 stub。
 *
 * 为什么需要：插件在真实运行时由 profile 的 node_modules 提供该依赖，而仓库里
 * `dsh-launcher/` 下没有 node_modules —— 不挂钩子，真插件代码在测试里根本 import 不进来。
 * 只影响这条 `--import` 命令行，产品的装载路径完全不受影响。
 */
const STUB = new URL("./schemastery-stub.mjs", import.meta.url).href;

export function resolve(specifier, context, nextResolve) {
	if (specifier === "@deepseek-ai/schemastery") {
		return { url: STUB, shortCircuit: true };
	}
	return nextResolve(specifier, context);
}
