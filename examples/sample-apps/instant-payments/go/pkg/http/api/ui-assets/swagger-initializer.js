// All code and the API document are served locally; disable remote validation.
window.onload = function () {
  window.ui = SwaggerUIBundle({
    url: "/api-docs",
    dom_id: "#swagger-ui",
    operationsSorter: "method",
    validatorUrl: null,
    presets: [SwaggerUIBundle.presets.apis],
    layout: "BaseLayout"
  });
};
