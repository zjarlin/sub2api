# 命令行入口

`cli.ts` 解析一键安装参数，`install.ts` 生成平台安装计划并校验目录，`config.ts` 将配置与模型列表写入选定的 Codex 数据目录。`--client cli --install-dir` 支持自定义安装位置；`--codex-home` 优先于环境变量 `CODEX_HOME`。
