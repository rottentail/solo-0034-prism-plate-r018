# 棱版产品与实现规范

用户为需要二维叠色校样的设计集成人员。核心工作是提交几何版面、冻结稿本、生成可追溯图像、校验并移交源稿。没有前端依赖，HTTP JSON是业务入口。

实体：Document（画布背景与尺寸）、Layer（隔离组透明度/混合方式）、Shape（局部轮廓与变换）、Brush（实色或渐变）、Revision（序号/摘要/时间）、Plate（版面历史）、Book（固定页序的拼版历史）、Placement（引用明确稿本的页面放置，可旋转与跨页）、PageArtifact（逐页图像及引脚轨迹）、Artifact（二进制内容及谱系）、Proof（终局校样）、Exposure（瞬态任务）、Envelope/Handover（带摘要的历史封包与逐页离线移交包）。

职责分工：scene定义契约（含页面、放置与跨页展开计划）；geom负责仿射和闭合轮廓展开；validate负责准入与估算预算；paint处理线性光合成；raster负责确定性覆盖采样与PNG，并暴露线性光画布供拼版复用；impose负责放置适配、跨页切片与越界/安全区判定；vector负责复用几何的安全SVG；repository负责原子快照、谱系校验与写入锁；exposure负责有界并发与取消；bundle负责封包摘要、版本和导入校验；handover负责逐页移交包与离线校验；service维护稿本、拼版与成品/校样关系；transport负责HTTP编解码与状态码；cmd负责启动恢复和关停。

主要不变量：旧稿本不可改写；同一expected_revision最多一个成功修订；渲染提交引用固定稿本；成品先完整计算后提交；被通过校样引用的成品不可撤销；被撤销的相同成品不得重新激活；导入不得覆盖已有版面；失败持久化不得产生内存成功；任何HTTP来源场景及恢复场景都必须通过同一编译准入；拼版放置只引用带摘要的明确稿本；页序交换随放置项移动且逐页引脚与实际绘制一致；跨页放置只适配、旋转一次再按页切片；越界与安全区结果显式报告而非静默修正；移交包条目确定且可离线重算。

生命周期：版面active→archived；拼版active→archived；页成品available→revoked；任务queued→running→succeeded/failed，queued/running可取消→cancelled；成品available→revoked；校样open→accepted/rejected；进程open→draining→closed；封包sealed→verified→imported/rejected，所有阶段由真实执行路径产生，无额外虚构状态字段。

验证边界：所有公开工作流均由scripts/walkthrough.py通过实际HTTP和临时持久化根检查正常与拒绝路径。PNG检查签名、尺寸、摘要与重复确定性；恢复检查重启前后成品内容；并发修订检查冲突与旧稿本保留。此为有界生产演示，不冒充完整视觉正确性证明。单元/性质/视觉回归测试全部延后。未接入远程服务，不需要私人数据库。

配置、容量、接口、复现输入与运维约束见README及docs。内部审计、任务题面、参考实现和标注材料不属于源仓库。
