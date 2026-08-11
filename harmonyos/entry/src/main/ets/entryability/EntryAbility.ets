import { AbilityConstant, ConfigurationConstant, UIAbility, Want } from '@kit.AbilityKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { window } from '@kit.ArkUI';
import { EcsRuntime } from '../model/EcsRuntime';

const DOMAIN = 0x454353;
const TAG = 'ECSHarmony';

export default class EntryAbility extends UIAbility {
  onCreate(_want: Want, _launchParam: AbilityConstant.LaunchParam): void {
    this.context.getApplicationContext().setColorMode(
      ConfigurationConstant.ColorMode.COLOR_MODE_DARK
    );
    hilog.info(DOMAIN, TAG, 'ECS HarmonyOS local node created');
  }

  onWindowStageCreate(windowStage: window.WindowStage): void {
    windowStage.loadContent('pages/Index', (error) => {
      if (error.code) {
        hilog.error(DOMAIN, TAG, 'Failed to load ECS page: %{public}s', JSON.stringify(error));
        return;
      }
      hilog.info(DOMAIN, TAG, 'ECS page loaded');
    });
  }

  onDestroy(): void {
    try {
      EcsRuntime.stop();
    } catch (error) {
      hilog.warn(DOMAIN, TAG, 'Failed to stop ECS node: %{public}s', `${error}`);
    }
  }
}
