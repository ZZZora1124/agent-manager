FROM iregistry.baidu-int.com/baidu-base/centos:7.5

# https://cloud.baidu-int.com/icloud/Appspace/最佳实践/使用GDP创建应用/

# 复制 output 下所有文件到工作目录，不包括 output 目录本身
COPY --chown=work output/ /home/work/

#  进入应用的根目录
WORKDIR /home/work

CMD ["bin/agent-runtime-manager"]

