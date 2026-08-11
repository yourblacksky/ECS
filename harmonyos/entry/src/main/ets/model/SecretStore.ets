import { asset } from '@kit.AssetStoreKit';
import { util } from '@kit.ArkTS';

const API_KEY_ALIAS: Uint8Array = new util.TextEncoder().encodeInto('ecs.llm.api_key');
const ADMIN_TOKEN_ALIAS: Uint8Array = new util.TextEncoder().encodeInto('ecs.admin.token');

export class SecretStore {
  private static load(alias: Uint8Array): string {
    const query: asset.AssetMap = new Map();
    query.set(asset.Tag.ALIAS, alias);
    query.set(asset.Tag.RETURN_TYPE, asset.ReturnType.ALL);
    try {
      const results: Array<asset.AssetMap> = asset.querySync(query);
      if (results.length === 0) {
        return '';
      }
      const value: asset.Value | undefined = results[0].get(asset.Tag.SECRET);
      if (value instanceof Uint8Array) {
        return new util.TextDecoder().decodeToString(value);
      }
    } catch (_) {
      // A missing Asset is the expected first-run state.
    }
    return '';
  }

  private static save(alias: Uint8Array, value: string): void {
    const attributes: asset.AssetMap = new Map();
    attributes.set(asset.Tag.ALIAS, alias);
    attributes.set(asset.Tag.SECRET, new util.TextEncoder().encodeInto(value));
    attributes.set(asset.Tag.CONFLICT_RESOLUTION, asset.ConflictResolution.OVERWRITE);
    asset.addSync(attributes);
  }

  static loadLLMAPIKey(): string {
    return SecretStore.load(API_KEY_ALIAS);
  }

  static saveLLMAPIKey(value: string): void {
    SecretStore.save(API_KEY_ALIAS, value);
  }

  static loadOrCreateAdminToken(): string {
    const existing: string = SecretStore.load(ADMIN_TOKEN_ALIAS);
    if (existing.length > 0) {
      return existing;
    }
    const token: string = `ecs-harmony-${util.generateRandomUUID(false)}`;
    SecretStore.save(ADMIN_TOKEN_ALIAS, token);
    return token;
  }
}
