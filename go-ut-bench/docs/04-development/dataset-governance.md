# 数据集治理说明（MVP）

## 1. 背景

仓库同时承载两类数据集资产：

1. 原始基线数据集：`datasets/python|go|java|cpp`
2. 目录化分级数据集：`datasets/l1|l2|l3`

当前治理目标是：

- 保持原始基线目录不变，作为长期稳定的源数据
- 通过可重复物化命令维护 `l1/l2/l3`
- 让 level 目录本身就能被评测链路直接消费

## 2. 运行时真相源

当前版本的运行时优先级：

1. `--dataset-manifest`
2. `--benchmark-profile`
3. `--level`
4. 直接扫描 `--dataset-root`

其中：

- `level` 的真相源是目录，不再是 manifest 标签
- `benchmark profile` 的真相源是代码内规则
- manifest 主要承担兼容入口和物化来源的角色

## 3. 目录约定

原始基线目录：

- `datasets/<lang>/<lang>_code_files_self_contained/<scenario>/<sample>.<ext>`
- `datasets/<lang>/<lang>_code_files_repo_level/<scenario>/<project>/...`

分级目录：

- `datasets/l1/<lang>/...`
- `datasets/l2/<lang>/...`
- `datasets/l3/<lang>/...`

`l1/l2/l3` 采用增量分层：

- `small = l1`
- `medium = l1 + l2`
- `large = l1 + l2 + l3`

## 4. 物化规则

通过以下命令重建目录：

```bash
cd go-ut-bench
./utbench dataset materialize-levels --dataset-root ../datasets --config-root ./configs
```

校验当前目录是否漂移：

```bash
cd go-ut-bench
./utbench dataset materialize-levels --dataset-root ../datasets --config-root ./configs --check
```

物化来源：

1. 优先使用 `configs/dataset_l1.json`、`dataset_l2.json`、`dataset_l3.json`
2. 若不存在，则从 `dataset_small.json`、`dataset_medium.json`、`dataset_large.json` 自动推导增量 level

复制策略：

- `self_contained`：复制样本文件
- `repo_level`：复制样本所属完整项目工作区

这条规则是强约束，不能只复制 repo-level 的目标文件。

## 5. 当前实现边界

- `dataset index` / `dataset manifest` 仍保留，用于兼容旧流程和构造清单
- `run` / `generate` 推荐优先使用 `--benchmark-profile` 或 `--level`
- `dataset_small.json`、`dataset_medium.json`、`dataset_large.json` 仍建议保留，作为兼容入口和 level 物化来源

## 6. 风险提示

1. 如果 level 清单缺失，当前实现会从 `small/medium/large` 反推，前提是三者满足严格子集关系。
2. repo-level 样本跨 level 重复时会带来明显的磁盘膨胀。
3. 目录被手工修改后，`materialize-levels --check` 会报漂移，需要重新物化。
