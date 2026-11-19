# HTTP契约

所有写入场景的JSON入口拒绝未知字段和尾随内容，最大8MiB。服务返回JSON错误；404为对象不存在，409为版本或生命周期冲突，429为成像容量不足，400为不符合输入契约，504为超时。未知路由由HTTP框架返回404。

| 方法与路径 | 输入及结果 |
|---|---|
| GET /healthz | ready |
| POST /v1/plates | id,title,document →201版面 |
| GET /v1/plates/{id} | 完整稿本历史 |
| PUT /v1/plates/{id} | expected_revision,document →追加稿本 |
| POST /v1/plates/{id}/branch | revision,id,title →201独立版面 |
| POST /v1/plates/{id}/archive | 禁止修订；历史仍可读取 |
| POST /v1/plates/{id}/render?revision=1&format=png | 201成品元数据；format也可为svg |
| POST /v1/plates/{id}/exposures?revision=1&format=png | 202瞬态任务 |
| GET /v1/exposures/{id} | queued/running/succeeded/failed/cancelled |
| DELETE /v1/exposures/{id} | 202取消请求 |
| GET /v1/artifacts/{id} | 图片；ETag为成品字节摘要；X-Scene-Digest为稿本摘要 |
| DELETE /v1/artifacts/{id} | 撤销成品 |
| POST /v1/proofs | id,artifact →201 open校样 |
| PUT /v1/proofs/{id} | state为accepted或rejected，note为说明 |
| GET /v1/plates/{id}/bundle | 含完整稿本历史的封包JSON |
| POST /v1/bundles | 原样封包；201导入，重名409 |

成品响应中的data为null，避免重复传输二进制；GET成品获得实际内容。If-None-Match精确匹配强ETag时返回304。成功校样可从持久化快照确认，当前版本没有校样列表/详情接口。GET版面返回完整历史，服务没有全局列表接口。

## 多页拼版

版面（plate）保持单页成像入口不变；拼版以 book 为一等实体，把明确稿本固定到固定页序的校样页上。

| 方法与路径 | 输入及结果 |
|---|---|
| POST /v1/books | id,title,pages → 201 book；每页含尺寸、背景、safe_margin 与 placements |
| GET /v1/books/{id} | 完整拼版历史 |
| PUT /v1/books/{id} | expected_revision,pages → 追加不可变拼版稿本 |
| POST /v1/books/{id}/reorder | expected_revision,order（原页号数组）→ 201 新稿本；页随其放置项移动 |
| POST /v1/books/{id}/archive | 禁止后续拼版稿本；历史仍可成像与移交 |
| POST /v1/books/{id}/render?revision=1 | 201 全部页成品；带 page=N 时只成像该页，重复请求为 200 且 created=false |
| GET /v1/pages/{id} | 该页 PNG；ETag 为图像摘要，X-Book-Digest 为拼版稿本摘要 |
| DELETE /v1/pages/{id} | 撤销该页成品；撤销后不可读 |
| GET /v1/books/{id}/handover?revision=1 | 200 application/zip 离线移交包；ETag 为拼版稿本摘要 |

放置项 `placement` 字段：plate、revision、可选 digest、x、y、width、height、rotation（度，正值在屏上顺时针）、fit（contain/cover/stretch）与可选 span。空 digest 在准入时按当前事实补全；显式 digest 与稿本不符时返回 409，不会静默改指新稿本。`span>1` 表示跨页：整块按拼版总宽只适配一次、只旋转一次，再按页边界切片；锚页报 `role=anchor`，后续页报 `role=continuation` 并共享同一变换。跨页放置要求所跨页面 trim 尺寸一致，且不可被 reorder 拆开。

每次成像在 `report.placements` 中给出可解释结果：`scale_x/scale_y`、未旋转内容矩形 `content`、旋转后轴对齐包围 `bounds`、`clipped_area`（越出 trim 的面积）、`overflow`（四边越界距离）、`exceeds_safe_area`、`letterbox` 与 `off_page`；页级另有 `safe_violated`。放置不会被自动挪动，越界是可见结果而非静默修正。

成品标识由拼版、稿本号、稿本摘要、页号与图像摘要共同决定；页序交换后受影响页的标识随之改变，且每页记录的 `sources`/`report` 仍指向实际绘制它的稿本引脚，因此换页不会错配审阅来源。

移交包为存储式（store）ZIP，含 `manifest.json`、`pages/page-NNN.png` 与 `sources/<plate>-rNNN.json`。manifest 记录拼版引用、每页声明原样（scene.Page）、每页引脚轨迹与放置报告、源稿文档及其摘要；条目顺序、时间戳与权限固定，相同输入得到相同字节。`prismplate -verify-handover <file>` 可在无数据根、无服务的情况下重算全部摘要并核验页号、引脚与源稿一致性；篡改任一条目都会失败。
