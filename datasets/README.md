# datasets

`datasets/` 是 go-ut-bench 的本地数据集根目录。

## 当前目录约定

- 原始基线数据集保留在：
  - `datasets/python`
  - `datasets/go`
  - `datasets/java`
  - `datasets/cpp`
- 目录化分级数据集维护在：
  - `datasets/l1`
  - `datasets/l2`
  - `datasets/l3`

其中：

- 原始基线目录不移动、不改名，用作物化 level 目录的源数据。
- `l1/l2/l3` 是可直接评测的 dataset root。
- `small/medium/large` 规格由代码按 `l1`、`l1+l2`、`l1+l2+l3` 组合解析，不再依赖固定 manifest 作为运行时真相源。

## 标准结构

每个语言目录内部继续沿用原有结构：

- `datasets/<root>/<lang>/self_contained/<scenario>/<sample_file>.<ext>`
- `datasets/<root>/<lang>/repo_level/<scenario>/<project>/...`

其中，`self_contained` 目录下的物理文件名使用短名（例如 `041.py`），
运行时逻辑 `sample_id` 仍保持为 `<scenario>_<编号>`（例如 `simple_function_041`）。

这里的 `<root>` 可以是原始根，也可以是 `l1` / `l2` / `l3`。

固定场景：

- `boundary`
- `simple_function`
- `complex_dependency`
- `interface_mock`

## level 目录的维护方式

`datasets/l1~l3` 通过 CLI 物化生成：

```bash
cd go-ut-bench
./utbench dataset materialize-levels --dataset-root ../datasets --config-root ./configs
```

校验当前 level 目录是否漂移：

```bash
cd go-ut-bench
./utbench dataset materialize-levels --dataset-root ../datasets --config-root ./configs --check
```

当前实现支持两种来源：

1. 若存在 `configs/dataset_l1.json`、`dataset_l2.json`、`dataset_l3.json`，优先按 level 清单物化。
2. 若不存在，则从现有 `dataset_small.json`、`dataset_medium.json`、`dataset_large.json` 反推出增量分层：
   - `l1 = small`
   - `l2 = medium - small`
   - `l3 = large - medium`

## repo_level 规则

- `self_contained`：复制样本文件本身
- `repo_level`：复制样本所属的完整项目工作区
- `.utbench`、`meta.json`、构建文件、依赖声明等评测所需文件会一并保留

这样做的目的是让 `datasets/l1~l3` 本身就能直接作为 dataset root 被 runner/evaluator/Docker 使用。

## 运行优先级

运行时数据集选择优先级为：

1. `--dataset-manifest`
2. `--benchmark-profile`
3. `--level`
4. `--dataset-root` 直接扫描

示例：

```bash
cd go-ut-bench

# 直接跑 l1
./utbench run --dataset-root ../datasets --level l1 --models deepseek --langs python --config ./configs/models.yaml

# 跑 medium 规格（自动组合 l1+l2）
./utbench run --dataset-root ../datasets --benchmark-profile medium --models deepseek --config ./configs/models.yaml
```
