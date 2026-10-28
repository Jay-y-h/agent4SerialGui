export namespace main {
	
	export class AppState {
	    activeMode: string;
	    status: string;
	    agentId: string;
	    gateway: string;
	    device: string;
	    logs: string[];
	
	    static createFrom(source: any = {}) {
	        return new AppState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.activeMode = source["activeMode"];
	        this.status = source["status"];
	        this.agentId = source["agentId"];
	        this.gateway = source["gateway"];
	        this.device = source["device"];
	        this.logs = source["logs"];
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

