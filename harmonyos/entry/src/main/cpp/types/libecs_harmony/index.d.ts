export const start: (dataDir: string, secretsJSON?: string) => string;
export const stop: () => string;
export const statusJSON: () => string;
export const setLLMAPIKey: (apiKey: string) => string;
export const discoveryAnnouncement: () => string;
export const pushDiscoveryAnnouncement: (payload: string) => string;
