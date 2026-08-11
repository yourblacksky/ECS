import { router } from '@kit.ArkUI';
import { EcsRuntime, NodeStatus } from '../model/EcsRuntime';
import { SecretStore } from '../model/SecretStore';

const COLOR_ABYSS = '#071421';
const COLOR_PANEL = '#0D2233';
const COLOR_PANEL_RAISED = '#123149';
const COLOR_LINE = '#24516A';
const COLOR_TEXT = '#F1F7FB';
const COLOR_MUTED = '#8EA7B7';
const COLOR_FLOW = '#55C7FF';
const COLOR_BLUE = '#2D7FF9';
const COLOR_WAIT = '#F6C85F';
const COLOR_FAULT = '#FF6B6B';

@Component
struct SignalRail {
  build() {
    Row() {
      Text('DEVICE').fontFamily('monospace').fontSize(10).fontColor(COLOR_MUTED)
      Text('●━━━━●').fontFamily('monospace').fontSize(10).fontColor(COLOR_FLOW)
      Text('ECS').fontFamily('monospace').fontSize(10).fontColor(COLOR_TEXT)
      Text('●━━━━●').fontFamily('monospace').fontSize(10).fontColor(COLOR_FLOW)
      Text('NETWORK').fontFamily('monospace').fontSize(10).fontColor(COLOR_MUTED)
    }
    .width('100%')
    .justifyContent(FlexAlign.SpaceBetween)
    .padding({ top: 14, bottom: 4 })
  }
}

@Component
struct MetricCard {
  @Prop label: string = '';
  @Prop value: string = '—';

  build() {
    Column() {
      Text(this.label).fontSize(10).fontColor(COLOR_MUTED)
      Text(this.value)
        .fontSize(18)
        .fontWeight(FontWeight.Bold)
        .fontColor(COLOR_TEXT)
        .margin({ top: 5 })
        .maxLines(1)
    }
    .alignItems(HorizontalAlign.Start)
    .padding(12)
    .backgroundColor(COLOR_PANEL_RAISED)
    .borderRadius(14)
    .layoutWeight(1)
  }
}

@Entry
@Component
struct Index {
  @State nodeStatus: NodeStatus = new NodeStatus();
  @State statusLabel: string = '正在启动';
  @State statusColor: string = COLOR_WAIT;
  @State errorMessage: string = '';
  @State apiKey: string = '';
  @State modelMessage: string = '';
  @State busy: boolean = false;

  aboutToAppear(): void {
    this.startNode();
  }

  private startNode(): void {
    if (this.busy) {
      return;
    }
    this.busy = true;
    this.statusLabel = '正在启动';
    this.statusColor = COLOR_WAIT;
    this.errorMessage = '';
    try {
      const storedAPIKey: string = SecretStore.loadLLMAPIKey();
      this.nodeStatus = EcsRuntime.start(this, storedAPIKey);
      this.statusLabel = this.nodeStatus.running ? '本地节点运行中' : '节点未运行';
      this.statusColor = this.nodeStatus.running ? COLOR_FLOW : COLOR_WAIT;
    } catch (error) {
      this.statusLabel = '启动失败';
      this.statusColor = COLOR_FAULT;
      this.errorMessage = `${error}`;
    } finally {
      this.busy = false;
    }
  }

  private configureModel(): void {
    const value: string = this.apiKey.trim();
    if (value.length === 0) {
      this.modelMessage = '请输入 DeepSeek API Key';
      return;
    }
    try {
      SecretStore.saveLLMAPIKey(value);
      this.nodeStatus = EcsRuntime.setLLMAPIKey(value);
      this.apiKey = '';
      this.modelMessage = '密钥已安全保存，模型对话已就绪';
    } catch (error) {
      this.modelMessage = `保存失败：${error}`;
    }
  }

  private refreshNode(): void {
    this.nodeStatus = EcsRuntime.status();
    this.statusLabel = this.nodeStatus.running ? '本地节点运行中' : '节点未运行';
    this.statusColor = this.nodeStatus.running ? COLOR_FLOW : COLOR_WAIT;
  }

  build() {
    Scroll() {
      Column() {
        Row() {
          Column() {
            Text('HARMONYOS EXECUTION NODE')
              .fontSize(10)
              .fontFamily('monospace')
              .letterSpacing(1.5)
              .fontColor(COLOR_MUTED)
            Text('ECS')
              .fontSize(38)
              .fontWeight(FontWeight.Bold)
              .fontColor(COLOR_TEXT)
              .margin({ top: 4 })
            Text('设备即节点 · 能力在本机执行')
              .fontSize(14)
              .fontColor(COLOR_MUTED)
              .margin({ top: 2 })
          }
          .alignItems(HorizontalAlign.Start)
          .layoutWeight(1)

          Row() {
            Circle().width(8).height(8).fill(this.statusColor)
            Text(this.statusLabel)
              .fontSize(11)
              .fontColor(this.statusColor)
              .margin({ left: 7 })
          }
          .padding({ left: 11, right: 11, top: 8, bottom: 8 })
          .backgroundColor(COLOR_PANEL_RAISED)
          .borderRadius(20)
        }
        .width('100%')
        .alignItems(VerticalAlign.Top)

        SignalRail()

        Column() {
          Text(this.nodeStatus.node)
            .fontSize(22)
            .fontWeight(FontWeight.Bold)
            .fontColor(COLOR_TEXT)
          Text(this.nodeStatus.node_id.length > 0 ? this.nodeStatus.node_id : '本机前台 ECS Peer')
            .fontSize(10)
            .fontFamily('monospace')
            .fontColor(COLOR_MUTED)
            .margin({ top: 6 })
            .maxLines(1)
          Divider().color(COLOR_LINE).margin({ top: 15, bottom: 13 })
          Row() {
            MetricCard({ label: 'TOOLS', value: `${this.nodeStatus.tools}` })
            Blank().width(9)
            MetricCard({ label: 'SKILLS', value: `${this.nodeStatus.skills}` })
            Blank().width(9)
            MetricCard({ label: 'PIPELINES', value: `${this.nodeStatus.pipelines}` })
          }
          .width('100%')
        }
        .width('100%')
        .alignItems(HorizontalAlign.Start)
        .padding(17)
        .backgroundColor(COLOR_PANEL)
        .border({ width: 1, color: COLOR_LINE })
        .borderRadius(20)
        .margin({ top: 14 })

        Column() {
          Text('LOCAL RUNTIME')
            .fontSize(10)
            .fontFamily('monospace')
            .letterSpacing(1.2)
            .fontColor(COLOR_FLOW)
          Text('鸿蒙设备内置 ECS')
            .fontSize(21)
            .fontWeight(FontWeight.Bold)
            .fontColor(COLOR_TEXT)
            .margin({ top: 5, bottom: 4 })
          Text('完整 Runtime、Manifest、Skill、Office Stage 和会话都运行并保存在本机应用沙箱。')
            .fontSize(12)
            .fontColor(COLOR_MUTED)
            .lineHeight(19)

          Row() {
            Circle()
              .width(8)
              .height(8)
              .fill(this.nodeStatus.model_ready ? COLOR_FLOW : COLOR_WAIT)
            Text(this.nodeStatus.model_ready ? 'DeepSeek 对话已就绪' : '需要配置 DeepSeek API Key')
              .fontSize(11)
              .fontColor(this.nodeStatus.model_ready ? COLOR_FLOW : COLOR_WAIT)
              .margin({ left: 8 })
          }
          .margin({ top: 13 })

          Row() {
            TextInput({
              placeholder: this.nodeStatus.model_ready ? '更新 DeepSeek API Key' : 'DeepSeek API Key',
              text: this.apiKey
            })
              .type(InputType.Password)
              .showPasswordIcon(true)
              .fontSize(12)
              .fontColor(COLOR_TEXT)
              .placeholderColor(COLOR_MUTED)
              .backgroundColor(COLOR_ABYSS)
              .border({ width: 1, color: COLOR_LINE })
              .borderRadius(12)
              .height(46)
              .layoutWeight(1)
              .onChange((value: string) => {
                this.apiKey = value;
              })
            Button(this.nodeStatus.model_ready ? '更新' : '保存')
              .fontSize(12)
              .fontWeight(FontWeight.Bold)
              .fontColor(COLOR_TEXT)
              .backgroundColor(COLOR_BLUE)
              .borderRadius(12)
              .height(46)
              .margin({ left: 9 })
              .onClick(() => this.configureModel())
          }
          .width('100%')
          .margin({ top: 11 })

          if (this.modelMessage.length > 0) {
            Text(this.modelMessage)
              .fontSize(10)
              .fontColor(this.nodeStatus.model_ready ? COLOR_FLOW : COLOR_FAULT)
              .margin({ top: 7 })
          }

          Row() {
            Text('地址').fontSize(11).fontColor(COLOR_MUTED)
            Text(EcsRuntime.endpoint)
              .fontSize(12)
              .fontFamily('monospace')
              .fontColor(COLOR_TEXT)
              .margin({ left: 12 })
          }
          .width('100%')
          .padding(13)
          .backgroundColor(COLOR_ABYSS)
          .border({ width: 1, color: COLOR_LINE })
          .borderRadius(13)
          .margin({ top: 15 })

          if (this.errorMessage.length > 0) {
            Text(this.errorMessage)
              .fontSize(11)
              .fontColor(COLOR_FAULT)
              .lineHeight(17)
              .width('100%')
              .margin({ top: 10 })
          }

          Row() {
            Button(this.nodeStatus.running ? '随应用运行' : '启动节点')
              .enabled(!this.busy)
              .fontSize(14)
              .fontWeight(FontWeight.Bold)
              .fontColor(COLOR_ABYSS)
              .backgroundColor(this.nodeStatus.running ? COLOR_PANEL_RAISED : COLOR_FLOW)
              .borderRadius(13)
              .height(48)
              .layoutWeight(1)
              .onClick(() => {
                if (!this.nodeStatus.running) {
                  this.startNode();
                }
              })
            Blank().width(10)
            Button('打开后台')
              .enabled(this.nodeStatus.running)
              .fontSize(14)
              .fontWeight(FontWeight.Medium)
              .fontColor(COLOR_TEXT)
              .backgroundColor(COLOR_BLUE)
              .borderRadius(13)
              .height(48)
              .layoutWeight(1)
              .onClick(() => router.pushUrl({ url: 'pages/Admin' }))
          }
          .width('100%')
          .margin({ top: 14 })

          Button('刷新运行状态')
            .enabled(!this.busy)
            .fontSize(12)
            .fontColor(COLOR_MUTED)
            .backgroundColor(Color.Transparent)
            .width('100%')
            .margin({ top: 4 })
            .onClick(() => this.refreshNode())
        }
        .width('100%')
        .alignItems(HorizontalAlign.Start)
        .padding(17)
        .backgroundColor(COLOR_PANEL)
        .border({ width: 1, color: COLOR_LINE })
        .borderRadius(20)
        .margin({ top: 14 })

        Row() {
          MetricCard({ label: 'AGENTS', value: `${this.nodeStatus.agents}` })
          Blank().width(9)
          MetricCard({ label: 'OFFICE STAGES', value: `${this.nodeStatus.office_components}` })
          Blank().width(9)
          MetricCard({ label: 'PLATFORM', value: this.nodeStatus.platform?.name ?? 'harmonyos' })
        }
        .width('100%')
        .margin({ top: 14, bottom: 28 })
      }
      .width('100%')
      .padding({ left: 18, right: 18, top: 28 })
    }
    .width('100%')
    .height('100%')
    .backgroundColor(COLOR_ABYSS)
    .scrollBar(BarState.Off)
  }
}
