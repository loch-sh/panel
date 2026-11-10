export interface MyUserModel {
  name: string;
  email: string;
  profile: UserProfile;
  permissions: string[];
}

export interface UserModel {
  email: string;
  name: string;
  profile: UserProfile;
  passwordHash?: string;
  permissions: string[];
}

export enum UserProfile {
  ADMIN = "admin",
  RESTRICTED = "restricted",
}

export enum UserActionEnum {
  SWITCH = "switch",
  RESTRICT = "restrict",
}
