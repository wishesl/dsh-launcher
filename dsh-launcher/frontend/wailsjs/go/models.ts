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
	    pid: number;
	    status: string;
	
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
	        this.pid = source["pid"];
	        this.status = source["status"];
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

}

