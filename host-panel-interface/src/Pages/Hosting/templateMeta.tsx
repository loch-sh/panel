import { CodeOutlined, FileTextOutlined } from "@ant-design/icons";
import { ReactNode } from "react";
import {
  HostingTemplate,
  templateHasPhp,
} from "../../Services/Hosting/Models/hosting.model";

/** Icon of a template: PHP runtime or static files. */
export function templateIcon(template: HostingTemplate): ReactNode {
  return templateHasPhp(template) ? <CodeOutlined /> : <FileTextOutlined />;
}

/** Tag color of a template, by web server. */
export function templateColor(template: HostingTemplate): string {
  return template === "php-nginx" || template === "nginx" ? "green" : "volcano";
}
