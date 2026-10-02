const getBtnService = document.getElementById("get-btn-service");
const getBtnBalancer = document.getElementById("get-btn-balancer");
const postBtn = document.getElementById("post-btn");
const output = document.getElementById("output");

const API_URL = "/api/echo";
const SERVICE_BASE_URL =
	"http://backend.backend-nikumar1206.svc.cluster.local:8000";
const BALANCER_BASE_URL = "http://test-api.onloco.app";

getBtnService.addEventListener("click", async () => {
	try {
		const url = SERVICE_BASE_URL + API_URL;
		const response = await fetch(url);
		const data = await response.json();
		output.textContent = JSON.stringify(data, null, 2);
	} catch (error) {
		output.textContent = `Error: ${error.message}`;
	}
});

getBtnBalancer.addEventListener("click", async () => {
	try {
		const url = BALANCER_BASE_URL + API_URL;
		const response = await fetch(url);
		const data = await response.json();
		output.textContent = JSON.stringify(data, null, 2);
	} catch (error) {
		output.textContent = `Error: ${error.message}`;
	}
});

postBtn.addEventListener("click", async () => {
	try {
		const url = BALANCER_BASE_URL + API_URL;
		const response = await fetch(url, {
			method: "POST",
			headers: {
				"Content-Type": "application/json",
			},
			body: JSON.stringify({ message: "Hello from the frontend!" }),
		});
		const data = await response.json();
		output.textContent = JSON.stringify(data, null, 2);
	} catch (error) {
		output.textContent = `Error: ${error.message}`;
	}
});
