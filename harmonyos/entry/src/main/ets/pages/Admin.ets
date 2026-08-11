import { router } from '@kit.ArkUI';
import { webview } from '@kit.ArkWeb';
import { EcsRuntime } from '../model/EcsRuntime';

@Entry
@Component
struct Admin {
  private controller: webview.WebviewController = new webview.WebviewController();

  build() {
    Column() {
      Row() {
        Button('返回')
          .fontSize(14)
          .fontColor('#DCECF6')
          .backgroundColor('#123149')
          .onClick(() => router.back())
        Column() {
          Text('本机 ECS 管理后台')
            .fontSize(18)
            .fontWeight(FontWeight.Bold)
            .fontColor('#F1F7FB')
          Text(`${EcsRuntime.endpoint}/admin/`)
            .fontSize(10)
            .fontFamily('monospace')
            .fontColor('#8EA7B7')
            .maxLines(1)
        }
        .alignItems(HorizontalAlign.Start)
        .margin({ left: 12 })
      }
      .width('100%')
      .padding({ left: 16, right: 16, top: 14, bottom: 12 })
      .backgroundColor('#0A1B2A')

      Web({
        src: `${EcsRuntime.endpoint}/admin/`,
        controller: this.controller
      })
        .javaScriptAccess(true)
        .domStorageAccess(true)
        .onPageEnd(() => {
          const token: string = JSON.stringify(EcsRuntime.adminToken);
          this.controller.runJavaScript(
            `if (sessionStorage.getItem("ecs-admin-token") !== ${token}) {` +
            `sessionStorage.setItem("ecs-admin-token", ${token}); location.reload(); }`
          );
        })
        .width('100%')
        .layoutWeight(1)
    }
    .width('100%')
    .height('100%')
    .backgroundColor('#071421')
  }
}
