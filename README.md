# 棱版 Prism Plate

Go 二维打样渲染服务。HTTP 接收不可变稿本，输出具有内容摘要的 PNG 和 SVG，支持有界异步成像、校样裁决与可移交版面封包。领域为图形渲染，产品形态为 backend；依赖只有 Go 标准库。

## 启动

需要 Go 1.24 或更新版本；本次验证环境为 Go 1.27.0 / macOS arm64。演示流程还需要 Python 3 标准库。构建和运行无需网络。

```sh
go build -o bin/prismplate ./cmd/prismplate
./bin/prismplate -listen 127.0.0.1:7846 -data var/prism-plate-v1
```

```sh
curl -H 'Content-Type: application/json' --data-binary @examples/amber-proof.json http://127.0.0.1:7846/v1/plates
curl -X POST 'http://127.0.0.1:7846/v1/plates/amber-plate/render?revision=1&format=png'
```

第二个响应返回成品 id；以 GET /v1/artifacts/{id} 获取图片。服务不依赖浏览器、GPU、字体或外部图片。默认只监听回环地址，无身份认证；运行边界和容器挂载见 docs/operations.md。

## 多页拼版与离线移交

单页版面之外，book 把明确稿本固定到固定页序的校样页上。放置项以 plate/revision（可带 digest）引用稿本，可旋转并按 contain/cover/stretch 适配，safe_margin 为裁切安全区；放置不会被自动挪动，越界与裁切面积以报告形式逐项给出。

```sh
curl -H 'Content-Type: application/json' --data-binary @examples/review-book.json http://127.0.0.1:7846/v1/books
curl -X POST 'http://127.0.0.1:7846/v1/books/review-book-a/render?revision=1'
curl -o delivery.zip 'http://127.0.0.1:7846/v1/books/review-book-a/handover?revision=1'
./bin/prismplate -verify-handover delivery.zip
```

移交包为确定性 ZIP：`manifest.json`、逐页 `pages/page-NNN.png` 与逐源稿 `sources/*.json`。manifest 把每页映射到实际绘制它的稿本引脚与放置报告，离线即可重算摘要。跨页放置（span>1）整块只适配、旋转一次再按页切片；交换页序时页随其放置项移动，来源不会错配。原版面与既有单页入口不变。

## 场景约定

examples/amber-proof.json 提供曲线、线性渐变、仿射变换、镂空、裁域、二值蒙版和 screen 叠色。坐标原点在左上，颜色为 0..1 的 sRGB 直通色值，PNG 在内部转换为线性光预乘透明度。M/L/Q/C/Z 路径必须显式闭合，轮廓至少三条端点指令；Q/C 分别固定细分16/24段。图层可选 normal/multiply/screen，组透明度在整层合成后施加，蒙版与裁域处于版面坐标，笔刷与填充轮廓处于形状局部坐标。未提供的数值使用 JSON 的零值；形状必须明确传入可逆 transform。

PNG 是像素校样的权威结果：每像素四点覆盖采样，画布最大512×512。SVG 是复用展开几何的矢量校样；浏览器在覆盖率与透明渐变插值方面可能产生差异，不承诺逐像素相同。所有数值和引用均由服务生成，SVG不接受任意标记、脚本或外链。

## 版本及生命周期

新建版面的初始稿本为1。PUT 传 expected_revision，只有当前最新版本相符才追加；旧版本不修改。归档禁止后续修订，仍可渲染历史及从历史分支。异步任务提交即捕获指定稿本；稍后修订不会改变它。任务只在当前进程内保留，重启后以已持久化成品为事实，不支持任务续跑；256条任务记录用尽后拒绝新任务。两名工作者、8条等待队列，所有渲染共用2个计算名额，同步或异步均可能在拥挤时得到拒绝。

取消是请求，已进入成品提交点的任务仍可成功；成功任务的事实不会变为取消。取消或失败不得提交半张图片。成品按版面、稿本、格式、内容摘要产生稳定标识；撤销后不允许再次悄悄激活。校样只有 open→accepted/rejected；通过的校样禁止撤销其成品。

## 入口

详见 docs/api.md。保存版面、修订、分支、归档、PNG、SVG、异步提交、取消、成品读取/撤销、校样、封包、恢复及写入隔离分别有独立的有界生产流程：

```sh
python3 scripts/walkthrough.py --workflow png
python3 scripts/walkthrough.py --workflow bundle
```

可选workflow值：create、revise、branch、archive、png、svg、exposure、cancel、artifact、proof、bundle、restore、isolation、rejection、book。每个流程在临时数据根启动实际HTTP服务，完成后正常关停，不占用长期端口、不修改演示输入。

## 持久化与范围

完整状态以临时写入、文件fsync、原子替换提交，失败写入不会成为内存事实。数据根使用排他 writer.lock；干净退出会清除，异常退出后需人工确认没有旧进程再清除。摘要校验保护谱系一致性，不提供恶意篡改认证。启动还会重新编译持久化场景。封包只含版面稿本，不含成品、校样和任务；导入重名拒绝，不合并历史。

上线容量：128版面，每版64稿本，64本拼版（每本64稿本、每页最多32页），256成品与256页成品，1024校样，状态快照128MiB，HTTP输入8MiB，单稿本4倍采样预计边遍历不超过1.6亿。容量达到时需要运维规划，不会自动淘汰被校样引用的数据。完整快照复制意味着吞吐适用于小规模打样服务。

自动化测试套件按初始化边界延后，本仓库不含测试命令。已提供构建、静态检查与真实生产流程演示；后续质量保障任务可以建立独立测试体系。Dockerfile提供容器构建路径，容器跨架构和生产部署不在本机Go构建的验证结论内。
