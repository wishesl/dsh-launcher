export namespace main {
	
	export class DSHVersion {
	    version: string;
	    published: string;
	    isLatest: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DSHVersion(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.published = source["published"];
	        this.isLatest = source["isLatest"];
	    }
	}
	export class ToolStatus {
	    name: string;
	    found: boolean;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new ToolStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.found = source["found"];
	        this.version = source["version"];
	    }
	}
	export class EnvReport {
	    npm: ToolStatus;
	    pnpm: ToolStatus;
	
	    static createFrom(source: any = {}) {
	        return new EnvReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.npm = this.convertValues(source["npm"], ToolStatus);
	        this.pnpm = this.convertValues(source["pnpm"], ToolStatus);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FavoriteDraft {
	    name: string;
	    owner: string;
	    url: string;
	    npm?: string;
	    category: string;
	    description: Record<string, string>;
	    stars?: number;
	    downloads?: number;
	    source: string;
	    spec: string;
	
	    static createFrom(source: any = {}) {
	        return new FavoriteDraft(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.owner = source["owner"];
	        this.url = source["url"];
	        this.npm = source["npm"];
	        this.category = source["category"];
	        this.description = source["description"];
	        this.stars = source["stars"];
	        this.downloads = source["downloads"];
	        this.source = source["source"];
	        this.spec = source["spec"];
	    }
	}
	export class FavoritePlugin {
	    id: string;
	    name: string;
	    owner: string;
	    url: string;
	    npm?: string;
	    install: string;
	    source: string;
	    category: string;
	    description: Record<string, string>;
	    stars?: number;
	    downloads?: number;
	    addedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new FavoritePlugin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.owner = source["owner"];
	        this.url = source["url"];
	        this.npm = source["npm"];
	        this.install = source["install"];
	        this.source = source["source"];
	        this.category = source["category"];
	        this.description = source["description"];
	        this.stars = source["stars"];
	        this.downloads = source["downloads"];
	        this.addedAt = source["addedAt"];
	    }
	}
	export class InstalledPlugin {
	    name: string;
	    spec: string;
	    version: string;
	    kind: string;
	    state: string;
	    description: string;
	    homepage: string;
	    github: string;
	    scope: string[];
	
	    static createFrom(source: any = {}) {
	        return new InstalledPlugin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.spec = source["spec"];
	        this.version = source["version"];
	        this.kind = source["kind"];
	        this.state = source["state"];
	        this.description = source["description"];
	        this.homepage = source["homepage"];
	        this.github = source["github"];
	        this.scope = source["scope"];
	    }
	}
	export class Instance {
	    id: string;
	    name: string;
	    directory: string;
	    version: string;
	    localVersion: string;
	    extraArgs: string;
	    pkgMgr: string;
	    autoStart: boolean;
	    // Go type: time
	    createdAt: any;
	    source: boolean;
	    initCmd: string;
	    buildCmd: string;
	    startCmd: string;
	    pid: number;
	    status: string;
	    webUrl: string;
	
	    static createFrom(source: any = {}) {
	        return new Instance(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.directory = source["directory"];
	        this.version = source["version"];
	        this.localVersion = source["localVersion"];
	        this.extraArgs = source["extraArgs"];
	        this.pkgMgr = source["pkgMgr"];
	        this.autoStart = source["autoStart"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.source = source["source"];
	        this.initCmd = source["initCmd"];
	        this.buildCmd = source["buildCmd"];
	        this.startCmd = source["startCmd"];
	        this.pid = source["pid"];
	        this.status = source["status"];
	        this.webUrl = source["webUrl"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class MarketPlugin {
	    name: string;
	    owner: string;
	    url: string;
	    category: string;
	    description: Record<string, string>;
	    npm?: string;
	    stars?: number;
	    downloads?: number;
	    install: string;
	    added: string;
	    deprecated: boolean;
	    replacement: string;
	
	    static createFrom(source: any = {}) {
	        return new MarketPlugin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.owner = source["owner"];
	        this.url = source["url"];
	        this.category = source["category"];
	        this.description = source["description"];
	        this.npm = source["npm"];
	        this.stars = source["stars"];
	        this.downloads = source["downloads"];
	        this.install = source["install"];
	        this.added = source["added"];
	        this.deprecated = source["deprecated"];
	        this.replacement = source["replacement"];
	    }
	}
	export class MarketCatalog {
	    updated: string;
	    count: number;
	    categories: Record<string, any>;
	    plugins: MarketPlugin[];
	
	    static createFrom(source: any = {}) {
	        return new MarketCatalog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.updated = source["updated"];
	        this.count = source["count"];
	        this.categories = source["categories"];
	        this.plugins = this.convertValues(source["plugins"], MarketPlugin);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class MarketOpResult {
	    ok: boolean;
	    cancelled: boolean;
	    already: boolean;
	    installed: string[];
	    blockedBuilds: string[];
	    output: string;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new MarketOpResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.cancelled = source["cancelled"];
	        this.already = source["already"];
	        this.installed = source["installed"];
	        this.blockedBuilds = source["blockedBuilds"];
	        this.output = source["output"];
	        this.error = source["error"];
	    }
	}
	
	export class MarketSettings {
	    registryUrl: string;
	    profile: string;
	
	    static createFrom(source: any = {}) {
	        return new MarketSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.registryUrl = source["registryUrl"];
	        this.profile = source["profile"];
	    }
	}
	export class ProxySettings {
	    proxy: string;
	
	    static createFrom(source: any = {}) {
	        return new ProxySettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.proxy = source["proxy"];
	    }
	}
	export class RegistryInfo {
	    package: string;
	    latest: string;
	    next: string;
	    source: string;
	    versions: DSHVersion[];
	
	    static createFrom(source: any = {}) {
	        return new RegistryInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.package = source["package"];
	        this.latest = source["latest"];
	        this.next = source["next"];
	        this.source = source["source"];
	        this.versions = this.convertValues(source["versions"], DSHVersion);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ServiceState {
	    instanceId: string;
	    url: string;
	    reachable: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ServiceState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.instanceId = source["instanceId"];
	        this.url = source["url"];
	        this.reachable = source["reachable"];
	    }
	}
	export class ShareImportResult {
	    imported: FavoritePlugin[];
	    skipped: string[];
	
	    static createFrom(source: any = {}) {
	        return new ShareImportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.imported = this.convertValues(source["imported"], FavoritePlugin);
	        this.skipped = source["skipped"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

