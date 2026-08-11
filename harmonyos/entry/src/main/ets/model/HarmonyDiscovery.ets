import { common } from '@kit.AbilityKit';
import { util } from '@kit.ArkTS';
import { mdns } from '@kit.NetworkKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import ecsHarmony from 'libecs_harmony.so';

const DOMAIN = 0x0000;
const TAG = 'ECSDiscovery';
const SERVICE_TYPE = '_ecs._tcp';
const REFRESH_INTERVAL_MS = 5000;

interface PlatformAnnouncement {
  name?: string;
  runtime_mode?: string;
  topology_role?: string;
  accepts_children?: boolean;
  supports_inbound?: boolean;
  supports_background?: boolean;
  supports_subprocess?: boolean;
  supports_multicast?: boolean;
  supports_push_wakeup?: boolean;
}

interface NodeAnnouncement {
  node_id?: string;
  node?: string;
  url?: string;
  platform?: PlatformAnnouncement;
  groups?: Array<string>;
  features?: Array<string>;
  capability_hash?: string;
}

interface ResolvedAnnouncement {
  protocol: string;
  node_id: string;
  node: string;
  url: string;
  platform: PlatformAnnouncement;
  groups: Array<string>;
  features: Array<string>;
  capability_hash: string;
  tools: Array<Object>;
}

export class HarmonyDiscovery {
  private static context: common.UIAbilityContext | undefined = undefined;
  private static discovery: mdns.DiscoveryService | undefined = undefined;
  private static registered: mdns.LocalServiceInfo | undefined = undefined;
  private static publishedPayload: string = '';
  private static selfNodeID: string = '';
  private static resolvedPeers: Map<string, string> = new Map();
  private static advertisementRefreshing: boolean = false;
  private static timerID: number = -1;

  static start(context: common.UIAbilityContext, selfNodeID: string): void {
    if (HarmonyDiscovery.discovery !== undefined) {
      return;
    }
    HarmonyDiscovery.context = context;
    HarmonyDiscovery.selfNodeID = selfNodeID;
    const discovery: mdns.DiscoveryService =
      mdns.createDiscoveryService(context, SERVICE_TYPE);
    discovery.on('serviceFound', (serviceInfo: mdns.LocalServiceInfo) => {
      hilog.info(
        DOMAIN,
        TAG,
        'mDNS service found: %{public}s',
        serviceInfo.serviceName
      );
      HarmonyDiscovery.resolveAndInject(serviceInfo);
    });
    discovery.on('serviceLost', (serviceInfo: mdns.LocalServiceInfo) => {
      HarmonyDiscovery.resolvedPeers.delete(HarmonyDiscovery.serviceKey(serviceInfo));
    });
    discovery.on('discoveryStart', (event: mdns.DiscoveryEventInfo) => {
      if (event.errorCode !== undefined) {
        hilog.warn(DOMAIN, TAG, 'mDNS discovery started with code %{public}d', event.errorCode);
      } else {
        hilog.info(DOMAIN, TAG, 'mDNS discovery started');
      }
    });
    discovery.on('discoveryStop', (event: mdns.DiscoveryEventInfo) => {
      if (event.errorCode !== undefined) {
        hilog.warn(DOMAIN, TAG, 'mDNS discovery stopped with code %{public}d', event.errorCode);
      }
    });
    discovery.startSearchingMDNS();
    HarmonyDiscovery.discovery = discovery;
    HarmonyDiscovery.refreshAdvertisement();
    HarmonyDiscovery.timerID = setInterval(() => {
      HarmonyDiscovery.refreshAdvertisement(true);
      HarmonyDiscovery.refreshResolvedPeers();
    }, REFRESH_INTERVAL_MS);
  }

  static stop(): void {
    if (HarmonyDiscovery.timerID >= 0) {
      clearInterval(HarmonyDiscovery.timerID);
      HarmonyDiscovery.timerID = -1;
    }
    if (HarmonyDiscovery.discovery !== undefined) {
      HarmonyDiscovery.discovery.stopSearchingMDNS();
      HarmonyDiscovery.discovery.off('serviceFound');
      HarmonyDiscovery.discovery.off('serviceLost');
      HarmonyDiscovery.discovery.off('discoveryStart');
      HarmonyDiscovery.discovery.off('discoveryStop');
      HarmonyDiscovery.discovery = undefined;
    }
    const context: common.UIAbilityContext | undefined = HarmonyDiscovery.context;
    const registered: mdns.LocalServiceInfo | undefined = HarmonyDiscovery.registered;
    if (context !== undefined && registered !== undefined) {
      mdns.removeLocalService(context, registered).catch((error: Error) => {
        hilog.warn(DOMAIN, TAG, 'remove mDNS service failed: %{public}s', `${error}`);
      });
    }
    HarmonyDiscovery.registered = undefined;
    HarmonyDiscovery.publishedPayload = '';
    HarmonyDiscovery.resolvedPeers.clear();
    HarmonyDiscovery.context = undefined;
  }

  private static refreshAdvertisement(force: boolean = false): void {
    const context: common.UIAbilityContext | undefined = HarmonyDiscovery.context;
    if (context === undefined || HarmonyDiscovery.advertisementRefreshing) {
      return;
    }
    const payload: string = ecsHarmony.discoveryAnnouncement();
    if (payload.length === 0 ||
      (!force && payload === HarmonyDiscovery.publishedPayload)) {
      return;
    }
    let announcement: NodeAnnouncement;
    try {
      announcement = JSON.parse(payload) as NodeAnnouncement;
    } catch (error) {
      hilog.warn(DOMAIN, TAG, 'ignore invalid local discovery payload: %{public}s', `${error}`);
      return;
    }
    const serviceInfo: mdns.LocalServiceInfo = {
      serviceType: SERVICE_TYPE,
      serviceName: HarmonyDiscovery.trim(announcement.node ?? 'ecs-harmony', 63),
      port: HarmonyDiscovery.portFromURL(announcement.url ?? ''),
      serviceAttribute: HarmonyDiscovery.attributesFromAnnouncement(
        announcement,
        Date.now().toString()
      )
    };
    if (serviceInfo.port === 0) {
      return;
    }
    HarmonyDiscovery.replaceAdvertisement(context, serviceInfo, payload);
  }

  private static async replaceAdvertisement(
    context: common.UIAbilityContext,
    serviceInfo: mdns.LocalServiceInfo,
    payload: string
  ): Promise<void> {
    HarmonyDiscovery.advertisementRefreshing = true;
    try {
      if (HarmonyDiscovery.registered !== undefined) {
        await mdns.removeLocalService(context, HarmonyDiscovery.registered);
      }
      HarmonyDiscovery.registered = await mdns.addLocalService(context, serviceInfo);
      HarmonyDiscovery.publishedPayload = payload;
      hilog.info(
        DOMAIN,
        TAG,
        'published %{public}s on port %{public}d',
        serviceInfo.serviceName,
        serviceInfo.port ?? 0
      );
    } catch (error) {
      hilog.warn(DOMAIN, TAG, 'publish mDNS service failed: %{public}s', `${error}`);
    } finally {
      HarmonyDiscovery.advertisementRefreshing = false;
    }
  }

  private static async resolveAndInject(serviceInfo: mdns.LocalServiceInfo): Promise<void> {
    const context: common.UIAbilityContext | undefined = HarmonyDiscovery.context;
    if (context === undefined) {
      return;
    }
    try {
      const resolved: mdns.LocalServiceInfo = await mdns.resolveLocalService(context, serviceInfo);
      const fields: Map<string, string> =
        HarmonyDiscovery.fieldsFromAttributes(resolved.serviceAttribute ?? []);
      if (fields.get('protocol') !== 'ecs1') {
        hilog.warn(
          DOMAIN,
          TAG,
          'ignore %{public}s without ECS protocol TXT',
          resolved.serviceName
        );
        return;
      }
      const nodeID: string = fields.get('node_id') ?? '';
      if (nodeID.length > 0 && nodeID === HarmonyDiscovery.selfNodeID) {
        return;
      }
      const address: string = resolved.host?.address ?? '';
      const port: number = resolved.port ?? 0;
      if (address.length === 0 || port === 0) {
        hilog.warn(
          DOMAIN,
          TAG,
          'ignore unresolved %{public}s address=%{public}s port=%{public}d',
          resolved.serviceName,
          address,
          port
        );
        return;
      }
      hilog.info(
        DOMAIN,
        TAG,
        'resolved %{public}s at %{public}s:%{public}d',
        resolved.serviceName,
        address,
        port
      );
      const announcement: ResolvedAnnouncement = {
        protocol: 'ecs/1',
        node_id: nodeID,
        node: fields.get('node') ?? resolved.serviceName,
        url: `http://${HarmonyDiscovery.hostPort(address, port)}`,
        platform: {
          name: fields.get('platform') ?? 'unknown',
          runtime_mode: fields.get('runtime_mode') ?? 'service',
          topology_role: fields.get('topology_role') ?? 'peer',
          accepts_children: fields.get('accepts_children') === '1',
          supports_inbound: fields.get('supports_inbound') === '1',
          supports_background: fields.get('supports_background') === '1',
          supports_subprocess: fields.get('supports_subprocess') === '1',
          supports_multicast: fields.get('supports_multicast') === '1',
          supports_push_wakeup: fields.get('supports_push_wakeup') === '1'
        },
        groups: HarmonyDiscovery.splitList(fields.get('groups') ?? ''),
        features: HarmonyDiscovery.splitList(fields.get('features') ?? ''),
        capability_hash: fields.get('capability_hash') ?? '',
        tools: []
      };
      const error: string =
        ecsHarmony.pushDiscoveryAnnouncement(JSON.stringify(announcement));
      if (error.length > 0) {
        hilog.warn(DOMAIN, TAG, 'inject mDNS peer failed: %{public}s', error);
        return;
      }
      HarmonyDiscovery.resolvedPeers.set(
        HarmonyDiscovery.serviceKey(serviceInfo),
        JSON.stringify(announcement)
      );
    } catch (error) {
      hilog.warn(DOMAIN, TAG, 'resolve mDNS service failed: %{public}s', `${error}`);
    }
  }

  private static refreshResolvedPeers(): void {
    for (const payload of HarmonyDiscovery.resolvedPeers.values()) {
      const error: string = ecsHarmony.pushDiscoveryAnnouncement(payload);
      if (error.length > 0) {
        hilog.warn(DOMAIN, TAG, 'renew mDNS peer failed: %{public}s', error);
      }
    }
  }

  private static serviceKey(serviceInfo: mdns.LocalServiceInfo): string {
    return `${serviceInfo.serviceType}|${serviceInfo.serviceName}`;
  }

  private static attributesFromAnnouncement(
    announcement: NodeAnnouncement,
    lease: string
  ): Array<mdns.ServiceAttribute> {
    const platform: PlatformAnnouncement = announcement.platform ?? {};
    const features: Array<string> =
      (announcement.features ?? []).filter((value: string) => value === 'network_auth');
    const pairs: Array<Array<string>> = [
      ['protocol', 'ecs1'],
      ['node', HarmonyDiscovery.trim(announcement.node ?? '', 96)],
      ['node_id', HarmonyDiscovery.trim(announcement.node_id ?? '', 80)],
      ['platform', HarmonyDiscovery.trim(platform.name ?? 'harmonyos', 32)],
      ['runtime_mode', HarmonyDiscovery.trim(platform.runtime_mode ?? 'foreground', 24)],
      ['topology_role', HarmonyDiscovery.trim(platform.topology_role ?? 'peer', 24)],
      ['accepts_children', HarmonyDiscovery.boolText(platform.accepts_children ?? false)],
      ['supports_inbound', HarmonyDiscovery.boolText(platform.supports_inbound ?? false)],
      ['supports_background', HarmonyDiscovery.boolText(platform.supports_background ?? false)],
      ['supports_subprocess', HarmonyDiscovery.boolText(platform.supports_subprocess ?? false)],
      ['supports_multicast', HarmonyDiscovery.boolText(platform.supports_multicast ?? false)],
      ['supports_push_wakeup', HarmonyDiscovery.boolText(platform.supports_push_wakeup ?? false)],
      ['groups', HarmonyDiscovery.trim((announcement.groups ?? []).join(','), 160)],
      ['features', features.join(',')],
      ['capability_hash', HarmonyDiscovery.trim(announcement.capability_hash ?? '', 64)],
      ['lease', lease]
    ];
    const attributes: Array<mdns.ServiceAttribute> = [];
    for (const pair of pairs) {
      attributes.push({
        key: pair[0],
        value: HarmonyDiscovery.encode(pair[1])
      });
    }
    return attributes;
  }

  private static fieldsFromAttributes(
    attributes: Array<mdns.ServiceAttribute>
  ): Map<string, string> {
    const result: Map<string, string> = new Map();
    for (const attribute of attributes) {
      result.set(attribute.key, new util.TextDecoder().decodeToString(
        new Uint8Array(attribute.value)
      ));
    }
    return result;
  }

  private static encode(value: string): Array<number> {
    const result: Array<number> = [];
    for (let index = 0; index < value.length; index++) {
      let codePoint: number = value.charCodeAt(index);
      if (codePoint >= 0xD800 && codePoint <= 0xDBFF &&
        index + 1 < value.length) {
        const low: number = value.charCodeAt(index + 1);
        if (low >= 0xDC00 && low <= 0xDFFF) {
          codePoint = 0x10000 + ((codePoint - 0xD800) << 10) + (low - 0xDC00);
          index++;
        }
      }
      if (codePoint <= 0x7F) {
        result.push(codePoint);
      } else if (codePoint <= 0x7FF) {
        result.push(0xC0 | (codePoint >> 6));
        result.push(0x80 | (codePoint & 0x3F));
      } else if (codePoint <= 0xFFFF) {
        result.push(0xE0 | (codePoint >> 12));
        result.push(0x80 | ((codePoint >> 6) & 0x3F));
        result.push(0x80 | (codePoint & 0x3F));
      } else {
        result.push(0xF0 | (codePoint >> 18));
        result.push(0x80 | ((codePoint >> 12) & 0x3F));
        result.push(0x80 | ((codePoint >> 6) & 0x3F));
        result.push(0x80 | (codePoint & 0x3F));
      }
    }
    return result;
  }

  private static portFromURL(value: string): number {
    const match: RegExpMatchArray | null = value.match(/:(\d+)(?:\/|$)/);
    if (match === null || match.length < 2) {
      return 0;
    }
    const port: number = Number.parseInt(match[1], 10);
    return port > 0 && port <= 65535 ? port : 0;
  }

  private static hostPort(address: string, port: number): string {
    if (address.includes(':') && !address.startsWith('[')) {
      return `[${address}]:${port}`;
    }
    return `${address}:${port}`;
  }

  private static boolText(value: boolean): string {
    return value ? '1' : '0';
  }

  private static splitList(value: string): Array<string> {
    if (value.trim().length === 0) {
      return [];
    }
    return value.split(',').map((item: string) => item.trim())
      .filter((item: string) => item.length > 0);
  }

  private static trim(value: string, limit: number): string {
    return value.length <= limit ? value : value.substring(0, limit);
  }
}
