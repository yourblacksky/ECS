import { common } from '@kit.AbilityKit';
import ecsHarmony from 'libecs_harmony.so';
import { HarmonyDiscovery } from './HarmonyDiscovery';
import { SecretStore } from './SecretStore';

export class PlatformStatus {
  name: string = 'harmonyos';
  runtime_mode: string = 'foreground';
  topology_role: string = 'peer';
}

export class NodeStatus {
  node: string = 'ecs-harmony';
  address: string = '127.0.0.1:18090';
  running: boolean = false;
  node_id: string = '';
  platform: PlatformStatus = new PlatformStatus();
  tools: number = 0;
  skills: number = 0;
  agents: number = 0;
  pipelines: number = 0;
  office_components: number = 0;
  model_ready: boolean = false;
  error: string = '';
}

export class EcsRuntime {
  static readonly endpoint: string = 'http://127.0.0.1:18090';
  static adminToken: string = '';
  private static lastStatus: NodeStatus = new NodeStatus();

  static start(owner: Object, apiKey: string = ''): NodeStatus {
    if (EcsRuntime.lastStatus.running) {
      return EcsRuntime.lastStatus;
    }
    const context = getContext(owner) as common.UIAbilityContext;
    const dataDir = `${context.filesDir}/ecs`;
    const adminToken: string = SecretStore.loadOrCreateAdminToken();
    const secrets: string = JSON.stringify({
      'llm.api_key': apiKey,
      'admin.token': adminToken
    });
    const result: string = ecsHarmony.start(dataDir, secrets);
    let status: NodeStatus;
    try {
      status = JSON.parse(result) as NodeStatus;
    } catch (_) {
      throw new Error(result.length > 0 ? result : 'ECS Runtime 未返回有效状态');
    }
    status.node = status.node ?? 'ecs-harmony';
    status.address = status.address ?? '127.0.0.1:18090';
    status.running = status.running ?? false;
    status.node_id = status.node_id ?? '';
    status.platform = status.platform ?? new PlatformStatus();
    status.tools = status.tools ?? 0;
    status.skills = status.skills ?? 0;
    status.agents = status.agents ?? 0;
    status.pipelines = status.pipelines ?? 0;
    status.office_components = status.office_components ?? 0;
    status.model_ready = status.model_ready ?? false;
    status.error = status.error ?? '';
    if (status.error.length > 0) {
      throw new Error(status.error);
    }
    EcsRuntime.lastStatus = status;
    EcsRuntime.adminToken = adminToken;
    HarmonyDiscovery.start(context, status.node_id);
    return status;
  }

  static status(): NodeStatus {
    return EcsRuntime.lastStatus;
  }

  static setLLMAPIKey(apiKey: string): NodeStatus {
    const error: string = ecsHarmony.setLLMAPIKey(apiKey);
    if (error.length > 0) {
      throw new Error(error);
    }
    EcsRuntime.lastStatus.model_ready = apiKey.trim().length > 0;
    return EcsRuntime.lastStatus;
  }

  static stop(): void {
    HarmonyDiscovery.stop();
    const error: string = ecsHarmony.stop();
    if (error.length > 0) {
      throw new Error(error);
    }
    EcsRuntime.lastStatus = new NodeStatus();
  }
}
