/**
 * `@deepseek-ai/schemastery` 的测试桩：插件对它的唯一用法是
 * `const Config = z.object({})`，这里只需要能返回一个配置 schema 对象。
 * 由 plugin-test-hooks.mjs 通过 `--import` 解析钩子挂上，不参与产品装载。
 */
export default {
	object: () => ({}),
};
