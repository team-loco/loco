lowest_run_port=20000
ephemeral_port_range=$(cat /proc/sys/net/ipv4/ip_local_port_range 2>/dev/null || sysctl -n net.inet.ip.portrange.first)
first_ephemeral_port=${ephemeral_port_range%%[[:space:]]*}

first_run_port() {
	local ports=$1
	echo $((lowest_run_port + $$ % ((first_ephemeral_port - lowest_run_port) / ports) * ports))
}
