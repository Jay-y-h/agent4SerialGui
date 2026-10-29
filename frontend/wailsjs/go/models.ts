export namespace main {
	
	export class DeviceState {
	    id: string;
	    name: string;
	    port: string;
	    state: string;
	
	    static createFrom(source: any = {}) {
	        return new DeviceState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.port = source["port"];
	        this.state = source["state"];
	    }
	}
	export class AppState {
	    activeMode: string;
	    status: string;
	    error: string;
	    agentId: string;
	    gateway: string;
	    gatewayState: string;
	    devices: DeviceState[];
	
	    static createFrom(source: any = {}) {
	        return new AppState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.activeMode = source["activeMode"];
	        this.status = source["status"];
	        this.error = source["error"];
	        this.agentId = source["agentId"];
	        this.gateway = source["gateway"];
	        this.gatewayState = source["gatewayState"];
	        this.devices = this.convertValues(source["devices"], DeviceState);
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
	
	export class Mode {
	    id: string;
	    name: string;
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new Mode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	    }
	}

}

